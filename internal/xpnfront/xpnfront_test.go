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

type ids struct{ profiles []xuacsapi.ConsoleProfile }

func (ids) Identity(_ context.Context, a string) (uint64, string, error) { return 0x0009000000000002, "Friend", nil }
func (ids) OnlineXUID(_ context.Context, a string) (uint64, error) {
	if a == "blocked" {
		return 0x0009000000000003, nil
	}
	return 0x0009000000000002, nil
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
