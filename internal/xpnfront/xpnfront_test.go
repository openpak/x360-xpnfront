package xpnfront

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openpak/x360-common/accountcore"
	"github.com/openpak/x360-common/wire"
	"github.com/openpak/x360-common/xuacsapi"
)

// Fixtures and expected replies carried over from x360-xuacs (friends_test.go,
// xpnfront_test.go): the console's request-0x2 body of 2026-10-18 and replies
// encoded independently with the XAM 17559 XRL codec.
const aliveFixture = "01040000790000000300000025e4cd2d00000000000000000000000000000000dbda39515f00090001000a00020f0000000000000050f22cca1c25e4cd2d0000000000000000000000000000000087724b7c6958a75911cd8eb7732e816c96609395fb733814000000000000000000000000000001000000000000000000000000000000000000000000000000000000000000000000000000"
const fixtureUser = "0009005f5139dadb:owner"

type graph struct {
	failure, empty bool
	owners         []string
	pending        bool
	changes        []string
}

func (c *graph) ListFriends(_ context.Context, a string) ([]accountcore.Friend, error) {
	c.owners = append(c.owners, a)
	if c.failure {
		return nil, errors.New("unavailable")
	}
	if c.empty || a != "owner" {
		return nil, nil
	}
	return []accountcore.Friend{{AccountID: "friend"}, {AccountID: "blocked"}, {AccountID: "hidden"}}, nil
}
func (c *graph) ListBlocks(_ context.Context, a string) ([]accountcore.Friend, error) {
	if c.empty || a != "owner" {
		return nil, nil
	}
	return []accountcore.Friend{{AccountID: "blocked"}}, nil
}
func (c *graph) CanDiscover(_ context.Context, a, b string) (bool, error) { return b != "hidden", nil }

func (c *graph) ListIncomingRequests(_ context.Context, a string) ([]accountcore.Friend, error) {
	if a == "owner" && c.pending {
		return []accountcore.Friend{{AccountID: "asker"}}, nil
	}
	return nil, nil
}
func (c *graph) ListOutgoingRequests(_ context.Context, a string) ([]accountcore.Friend, error) {
	if a == "owner" && c.pending {
		return []accountcore.Friend{{AccountID: "asked"}}, nil
	}
	return nil, nil
}
func (c *graph) Block(_ context.Context, a, b string) error {
	c.changes = append(c.changes, "block "+a+" "+b)
	return nil
}
func (c *graph) Unblock(_ context.Context, a, b string) error {
	c.changes = append(c.changes, "unblock "+a+" "+b)
	return nil
}

type ids struct{ profiles []xuacsapi.ConsoleProfile }

func (ids) Identity(_ context.Context, a string) (uint64, string, error) {
	return 0x0009000000000002, "Friend", nil
}
func (ids) OnlineXUID(_ context.Context, a string) (uint64, error) {
	if a == "blocked" {
		return 0x0009000000000003, nil
	}
	return 0x0009000000000002, nil
}
func (ids) AccountByXUID(_ context.Context, x uint64) (string, bool, error) {
	if x == 0x0009000000000003 {
		return "blocked", true, nil
	}
	return "", false, nil
}
func (i ids) ConsoleProfiles(context.Context, string) ([]xuacsapi.ConsoleProfile, error) {
	return i.profiles, nil
}

func TestPresenceAliveResponse(t *testing.T) {
	req, _ := hex.DecodeString(aliveFixture)
	want, _ := hex.DecodeString("01040000100000000300000025e4cd2d0000000000000000000000000000000000000000000000000000000000000000")
	if got := (&friendSnapshot{}).reply(req); !bytes.Equal(got, want) {
		t.Fatalf("got %x", got)
	}
}

func TestFriendsReplyMatchesNativeSchema(t *testing.T) {
	h := &Handler{Social: &graph{}, IDs: ids{}}
	req, _ := hex.DecodeString(aliveFixture)
	got, err := h.presenceFriends(context.Background(), req, fixtureUser)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("010400002a0000000300000025e4cd2d00000000000000000000000000000000000000008a3179250100c2d5a2ac01000200000000000900070000467269656e64000300000000000900")
	if !bytes.Equal(got, want) {
		t.Fatalf("native schema mismatch\n got %x\nwant %x", got, want)
	}
	h.Social = &graph{empty: true}
	empty, err := h.presenceFriends(context.Background(), req, fixtureUser)
	if err != nil || len(empty) != 48 || binary.LittleEndian.Uint32(empty[36:]) != 0 || binary.LittleEndian.Uint32(empty[42:]) != 0 {
		t.Fatalf("empty graph does not clear versions: %x %v", empty, err)
	}
}

func TestFriendsBindEachRequestToTicketUser(t *testing.T) {
	core := &graph{empty: true}
	h := &Handler{Social: core, IDs: ids{}}
	req, _ := hex.DecodeString(aliveFixture)
	second := append([]byte{}, req...)
	binary.LittleEndian.PutUint64(second[32:], 0x0009000000000042)
	binary.LittleEndian.PutUint32(second[8:], 9)
	got, err := h.presenceFriends(context.Background(), append(req, second...), fixtureUser+",0009000000000042:other")
	if err != nil || len(got) != 96 || strings.Join(core.owners, ",") != "owner,other" || binary.LittleEndian.Uint32(got[56:]) != 9 {
		t.Fatalf("account switching projection: owners=%v data=%x err=%v", core.owners, got, err)
	}
	for _, header := range []string{"", "0009000000000042:other", fixtureUser + "," + fixtureUser} {
		before := len(core.owners)
		if _, err := h.presenceFriends(context.Background(), req, header); err == nil || len(core.owners) != before {
			t.Errorf("foreign/invalid user queried graph: %q", header)
		}
	}
}

func TestPresenceMalformedAndCoreFailure(t *testing.T) {
	h := &Handler{Social: &graph{}, IDs: ids{}}
	req, _ := hex.DecodeString(aliveFixture)
	for _, body := range [][]byte{nil, req[:100], append(append([]byte{}, req...), 1)} {
		if _, err := h.presenceFriends(context.Background(), body, fixtureUser); err == nil {
			t.Errorf("malformed body accepted: %x", body)
		}
	}
	h.Social = &graph{failure: true}
	r := httptest.NewRequest(http.MethodPost, PathXPresence, bytes.NewReader(req))
	r.Header.Set(wire.UsersHeader, fixtureUser)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("core failure reported as successful graph: %d", w.Code)
	}
}

// A logoff after the ticket was replaced is accepted for a profile issued on the console.
func TestConsoleLogoff(t *testing.T) {
	body := make([]byte, 40)
	binary.LittleEndian.PutUint32(body, 0x400)
	binary.LittleEndian.PutUint32(body[4:], 8)
	binary.LittleEndian.PutUint64(body[32:], 0x0009000000000077)
	h := &Handler{IDs: ids{profiles: []xuacsapi.ConsoleProfile{{Account: "a", Online: 0x0009000000000077}}}}
	if err := h.consoleLogoff(context.Background(), body, "", "478700668470@xbox.com"); err != nil {
		t.Fatal(err)
	}
	if err := (&Handler{IDs: ids{}}).consoleLogoff(context.Background(), body, "", "478700668470@xbox.com"); err == nil {
		t.Fatal("logoff for a profile not on this console accepted")
	}
}

// Carried over from x360-xuacs agegroup_test.go.
func TestNativeLogoffAcknowledgement(t *testing.T) {
	h := &Handler{}
	req, _ := hex.DecodeString("00040000080000000300000025e4cd2d00000000000000000000000000000000dbda39515f000900")
	out, err := h.presenceFriends(context.Background(), req, fixtureUser)
	if err != nil || len(out) != 0 {
		t.Fatalf("logoff reply %x %v", out, err)
	}
	if _, err = h.presenceFriends(context.Background(), req, "0009000000000001:other"); err == nil {
		t.Fatal("accepted foreign logoff")
	}
	if _, err = h.presenceFriends(context.Background(), req[:39], fixtureUser); err == nil {
		t.Fatal("accepted truncated logoff")
	}
}

func TestLogoffAfterTicketUserRemoval(t *testing.T) {
	h := &Handler{IDs: ids{profiles: []xuacsapi.ConsoleProfile{{Account: "owner", Online: 0x0009005f5139dadb}}}}
	req, _ := hex.DecodeString("00040000080000000300000025e4cd2d00000000000000000000000000000000dbda39515f000900")
	if err := h.consoleLogoff(context.Background(), req, "", "478700668470@xbox.com"); err != nil {
		t.Fatal(err)
	}
	h.IDs = ids{}
	for _, console := range []string{"", "111111111111@xbox.com", "malformed"} {
		if err := h.consoleLogoff(context.Background(), req, "", console); err == nil {
			t.Errorf("accepted unbound console %q", console)
		}
	}
}

// Request/reply hex from re-friends-requests.md section 3 and the codec-checked
// minimal 0x17 reply.
const hdr = "30040000%s00000003000000" + "25e4cd2d00000000000000000000000000000000"

func xpHeader(typ uint32, msgLen uint32) []byte {
	h := make([]byte, 32)
	binary.LittleEndian.PutUint32(h, typ)
	binary.LittleEndian.PutUint32(h[4:], msgLen)
	binary.LittleEndian.PutUint32(h[8:], 3)
	sg, _ := hex.DecodeString("25e4cd2d00000000000000000000000000000000")
	copy(h[12:], sg)
	return h
}

func u64(b []byte, v uint64) []byte { return binary.LittleEndian.AppendUint64(b, v) }

func TestBlockUnblock(t *testing.T) {
	g := &graph{}
	h := &Handler{Social: g, IDs: ids{}}
	for _, typ := range []uint32{0x3F0, 0x3F1} {
		req := u64(u64(xpHeader(typ, 0x10), 0x0009005f5139dadb), 0x0009000000000003)
		out, err := h.presenceFriends(context.Background(), req, fixtureUser)
		if err != nil || len(out) != 0 {
			t.Fatalf("%x: %x %v", typ, out, err)
		}
	}
	if strings.Join(g.changes, ",") != "block owner blocked,unblock owner blocked" {
		t.Fatalf("changes %v", g.changes)
	}
	// owner not on the ticket
	req := u64(u64(xpHeader(0x3F0, 0x10), 0x0009000000000009), 0x0009000000000003)
	if _, err := h.presenceFriends(context.Background(), req, fixtureUser); err == nil {
		t.Fatal("foreign owner accepted")
	}
}

func TestPresenceBlob(t *testing.T) {
	schema := pgSchema(t)
	st := testState(t, schema)
	h := &Handler{Social: &graph{}, IDs: ids{}, State: st}
	user := uint64(0x0009005f5139dadb)
	get := u64(xpHeader(0x430, 8), user)
	want0, _ := hex.DecodeString("30040000060000000300000025e4cd2d00000000000000000000000000000000" + "00000000" + "0000")
	got, err := h.presenceFriends(context.Background(), get, fixtureUser)
	if err != nil || !bytes.Equal(got, want0) {
		t.Fatalf("empty get: %x %v", got, err)
	}
	blob := bytes.Repeat([]byte{0xAB}, 60)
	set := append(binary.LittleEndian.AppendUint16(u64(xpHeader(0x42F, 10+60), user), 60), blob...)
	got, err = h.presenceFriends(context.Background(), set, fixtureUser)
	want, _ := hex.DecodeString("30040000040000000300000025e4cd2d00000000000000000000000000000000" + "00000000")
	want[0], want[1] = 0x2F, 0x04
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("set: %x %v", got, err)
	}
	// persisted and reloaded
	h.State = testState(t, schema)
	got, err = h.presenceFriends(context.Background(), get, fixtureUser)
	if err != nil || len(got) != 38+60 || !bytes.Equal(got[38:], blob) || binary.LittleEndian.Uint32(got[4:]) != 66 || binary.LittleEndian.Uint16(got[36:]) != 60 {
		t.Fatalf("get: %x %v", got, err)
	}
}

func TestInvitesAndPendingStatus(t *testing.T) {
	h := &Handler{Social: &graph{pending: true}, IDs: ids{}}
	user := uint64(0x0009005f5139dadb)
	inv := binary.LittleEndian.AppendUint32(u64(u64(xpHeader(0x416, 0x14), user), 7), 0)
	if out, err := h.presenceFriends(context.Background(), inv, fixtureUser); err != nil || len(out) != 0 {
		t.Fatalf("0x207: %x %v", out, err)
	}
	ans := binary.LittleEndian.AppendUint16(append(u64(u64(xpHeader(0x3F4, 0x1A), user), 7), make([]byte, 8)...), 1)
	if out, err := h.presenceFriends(context.Background(), ans, fixtureUser); err != nil || len(out) != 0 {
		t.Fatalf("0x581: %x %v", out, err)
	}
	req, _ := hex.DecodeString(aliveFixture)
	got, err := h.presenceFriends(context.Background(), req, fixtureUser)
	if err != nil {
		t.Fatal(err)
	}
	var st []byte
	for rest := got[48:]; len(rest) >= 11; {
		st = append(st, rest[10])
		rest = rest[11+int(binary.LittleEndian.Uint16(rest[8:])):]
	}
	if string(st) != "\x00\x01\x02" {
		t.Fatalf("statuses %v", st)
	}
}

type fakeMail struct {
	n   int
	err error
}

func (f fakeMail) Unread(context.Context, uint64) (int, error) { return f.n, f.err }

func TestMailFlag(t *testing.T) {
	req, _ := hex.DecodeString(aliveFixture)
	hrOf := func(m Unreads) uint32 {
		h := &Handler{Social: &graph{empty: true}, IDs: ids{}, Mail: m}
		got, err := h.presenceFriends(context.Background(), req, fixtureUser)
		if err != nil {
			t.Fatal(err)
		}
		return binary.LittleEndian.Uint32(got[32:])
	}
	if hrOf(fakeMail{n: 2}) != 0x001512F0 || hrOf(fakeMail{}) != 0 || hrOf(fakeMail{err: errors.New("down")}) != 0 || hrOf(nil) != 0 {
		t.Fatal("mail flag")
	}
}

func TestUnreadClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Key") != "k" || r.URL.Path != "/internal/v1/unread" || r.URL.Query().Get("xuid") != "0009005f5139dadb" {
			http.Error(w, "no", 403)
			return
		}
		w.Write([]byte(`{"unread":3}`))
	}))
	defer srv.Close()
	n, err := (&UnreadClient{URL: srv.URL, Key: "k"}).Unread(context.Background(), 0x0009005f5139dadb)
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := (&UnreadClient{URL: srv.URL, Key: "bad"}).Unread(context.Background(), 1); err == nil {
		t.Fatal("bad key accepted")
	}
}
