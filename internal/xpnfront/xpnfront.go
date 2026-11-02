// Package xpnfront serves Xbox Live presence (Live service 1, /xpnfront): the
// presence-alive and logoff requests the console's XLive worker sends after the
// service-26 stream is up, answered with the user's friend and block lists from
// the account core (docs/protocol.md). Moved out of x360-xuacs v0.9.7.
package xpnfront

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openpak/x360-common/accountcore"
	"github.com/openpak/x360-common/wire"
	"github.com/openpak/x360-common/xuacsapi"
)

const (
	// PathXPresence is the presence endpoint (requests 0x2, 0x7, 0x9, 0xA, 0xF, ...).
	PathXPresence = "/xpnfront/xpresence.srf"
	ContentType   = "xon/5"
	maxBody       = 64 << 10

	// The native presence-alive request has a 32-byte header and one element per
	// user. Replies echo its sequence/network block and carry authoritative
	// friend and block list versions, counts and records.
	presenceHeader = 32
	presenceBody   = 16
)

// Social is the account core's social graph; xpnfront owns no friend graph.
type Social interface {
	ListFriends(context.Context, string) ([]accountcore.Friend, error)
	ListBlocks(context.Context, string) ([]accountcore.Friend, error)
	CanDiscover(context.Context, string, string) (bool, error)
	ListIncomingRequests(context.Context, string) ([]accountcore.Friend, error)
	ListOutgoingRequests(context.Context, string) ([]accountcore.Friend, error)
	Block(ctx context.Context, blocker, blocked string) error
	Unblock(ctx context.Context, blocker, blocked string) error
}

// Identities are the online XUIDs and gamertags x360-xuacs owns.
type Identities interface {
	Identity(ctx context.Context, account string) (uint64, string, error)
	OnlineXUID(ctx context.Context, account string) (uint64, error)
	ConsoleProfiles(ctx context.Context, console string) ([]xuacsapi.ConsoleProfile, error)
	AccountByXUID(ctx context.Context, xuid uint64) (string, bool, error)
}

// Handler serves PathXPresence. Wrap it with wire.Guard.
type Handler struct {
	Social Social
	IDs    Identities
	Log    *slog.Logger
	State  *State  // presence blobs (0x16/0x17); nil answers 0x17 empty
	Mail   Unreads // x360-xmessaging unread counts; nil = never flag mail

	warnMu sync.Mutex
	warned time.Time
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

type reqErr struct {
	status int
	msg    string
}

func (e *reqErr) Error() string           { return e.msg }
func deny(status int, msg string) *reqErr { return &reqErr{status, msg} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		return
	}
	if !strings.EqualFold(r.URL.Path, PathXPresence) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	var out []byte
	if len(body) >= 4 && binary.LittleEndian.Uint32(body) == 0x400 {
		err = h.consoleLogoff(r.Context(), body, r.Header.Get(wire.UsersHeader), r.Header.Get(wire.AccountHeader))
	} else {
		out, err = h.presenceFriends(r.Context(), body, r.Header.Get(wire.UsersHeader))
	}
	if err != nil {
		status := http.StatusServiceUnavailable
		var re *reqErr
		if errors.As(err, &re) {
			status = re.status
		}
		h.log().Warn("xpresence refused", "status", status, "body_len", len(body), "err", err)
		http.Error(w, http.StatusText(status), status)
		return
	}
	h.log().Info("xpresence reply", "account", r.Header.Get(wire.AccountHeader), "body_len", len(body), "reply_len", len(out))
	w.Header().Set("Content-Type", ContentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(http.StatusOK)
	w.Write(out)
}

// consoleDigits is the 12-digit console of a "<digits>@xbox.com" account, "" otherwise.
func consoleDigits(account string) string {
	d, _, _ := strings.Cut(account, "@")
	if len(d) != 12 {
		return ""
	}
	for _, c := range d {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return d
}

// A replacement console-only ticket can precede P_LOGOFF. In that case validate
// the departing XUID against profiles issued to that authenticated console,
// rather than requiring it to remain on the new ticket's active-user list.
func (h *Handler) consoleLogoff(ctx context.Context, body []byte, usersHeader, consoleHeader string) error {
	users, err := ticketUsers(usersHeader)
	if err != nil {
		users = map[uint64]string{}
	}
	if digits := consoleDigits(consoleHeader); digits != "" && h.IDs != nil {
		ps, err := h.IDs.ConsoleProfiles(ctx, digits)
		if err != nil {
			return deny(503, "account service unavailable")
		}
		for _, p := range ps {
			users[p.Online] = p.Account
		}
	}
	count := 0
	for len(body) > 0 {
		if len(body) < 40 || binary.LittleEndian.Uint32(body) != 0x400 || binary.LittleEndian.Uint32(body[4:]) != 8 {
			return deny(400, "invalid native logoff")
		}
		if users[binary.LittleEndian.Uint64(body[32:])] == "" {
			return deny(403, "logoff profile is not bound to this console")
		}
		body = body[40:]
		count++
		if count > 4 {
			return deny(400, "too many logoff entries")
		}
	}
	return nil
}

// Gateway user projections come from the authenticated tunnel's ticket. The
// gateway strips client-provided versions of these internal headers.
func ticketUsers(header string) (map[uint64]string, error) {
	users := make(map[uint64]string)
	for _, entry := range strings.Split(header, ",") {
		x, account, ok := strings.Cut(entry, ":")
		if !ok || len(x) != 16 || account == "" {
			return nil, deny(403, "missing tunnel user")
		}
		xuid, err := strconv.ParseUint(x, 16, 64)
		if err != nil || xuid>>48 != 9 || users[xuid] != "" {
			return nil, deny(403, "invalid tunnel user")
		}
		users[xuid] = account
	}
	if len(users) > 4 {
		return nil, deny(403, "too many tunnel users")
	}
	return users, nil
}

type friendRecord struct {
	xuid   uint64
	name   string
	status byte // 0 accepted, 1 outgoing request pending, 2 incoming request pending
}
type friendSnapshot struct {
	friends []friendRecord
	blocks  []uint64
	hr      uint32 // 0, or hrNewMail when the user has unread mail
}

func (h *Handler) friendSnapshot(ctx context.Context, account string) (*friendSnapshot, error) {
	if h.Social == nil || h.IDs == nil {
		return nil, errors.New("social account service unavailable")
	}
	friends, err := h.Social.ListFriends(ctx, account)
	if err != nil {
		return nil, err
	}
	blocks, err := h.Social.ListBlocks(ctx, account)
	if err != nil {
		return nil, err
	}
	s := &friendSnapshot{}
	blocked := make(map[string]bool)
	for _, b := range blocks {
		blocked[b.AccountID] = true
		xuid, err := h.IDs.OnlineXUID(ctx, b.AccountID)
		if err != nil {
			return nil, err
		}
		s.blocks = append(s.blocks, xuid)
	}
	seen := make(map[string]bool)
	for _, f := range friends {
		if f.AccountID == account || seen[f.AccountID] || blocked[f.AccountID] {
			continue
		}
		seen[f.AccountID] = true
		allowed, err := h.Social.CanDiscover(ctx, account, f.AccountID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		xuid, name, err := h.IDs.Identity(ctx, f.AccountID)
		if err != nil {
			return nil, err
		}
		if !nativeName(name) {
			return nil, errors.New("invalid native friend gamertag")
		}
		s.friends = append(s.friends, friendRecord{xuid, name, 0})
	}
	incoming, err := h.Social.ListIncomingRequests(ctx, account)
	if err != nil {
		return nil, err
	}
	outgoing, err := h.Social.ListOutgoingRequests(ctx, account)
	if err != nil {
		return nil, err
	}
	for status, list := range [][]accountcore.Friend{outgoing, incoming} {
		for _, f := range list {
			if f.AccountID == account || seen[f.AccountID] || blocked[f.AccountID] {
				continue
			}
			seen[f.AccountID] = true
			xuid, name, err := h.IDs.Identity(ctx, f.AccountID)
			if err != nil {
				return nil, err
			}
			if !nativeName(name) {
				return nil, errors.New("invalid native friend gamertag")
			}
			s.friends = append(s.friends, friendRecord{xuid, name, byte(status + 1)})
		}
	}
	if len(s.friends) > 100 || len(s.blocks) > 250 {
		return nil, errors.New("social graph exceeds native Xbox 360 limits")
	}
	sort.Slice(s.friends, func(i, j int) bool { return s.friends[i].xuid < s.friends[j].xuid })
	sort.Slice(s.blocks, func(i, j int) bool { return s.blocks[i] < s.blocks[j] })
	return s, nil
}

// nativeName: XAM permits at most 16 name bytes including the terminator.
func nativeName(name string) bool {
	if len(name) == 0 || len(name) > 15 {
		return false
	}
	for _, c := range []byte(name) {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func listVersion(b []byte) uint32 {
	if len(b) == 0 {
		return 0
	}
	sum := sha256.Sum256(b)
	v := binary.LittleEndian.Uint32(sum[:4])
	if v == 0 {
		return 1
	}
	return v
}

func (s *friendSnapshot) reply(e []byte) []byte {
	var friends, blocks []byte
	for _, f := range s.friends {
		friends = binary.LittleEndian.AppendUint64(friends, f.xuid)
		friends = binary.LittleEndian.AppendUint16(friends, uint16(len(f.name)+1))
		friends = append(friends, f.status)
		friends = append(friends, f.name...)
		friends = append(friends, 0)
	}
	for _, xuid := range s.blocks {
		blocks = binary.LittleEndian.AppendUint64(blocks, xuid)
	}
	out := append([]byte{}, e[:presenceHeader]...)
	binary.LittleEndian.PutUint32(out[4:], uint32(presenceBody+len(friends)+len(blocks)))
	out = binary.LittleEndian.AppendUint32(out, s.hr)
	out = binary.LittleEndian.AppendUint32(out, listVersion(friends))
	out = binary.LittleEndian.AppendUint16(out, uint16(len(s.friends)))
	out = binary.LittleEndian.AppendUint32(out, listVersion(blocks))
	out = binary.LittleEndian.AppendUint16(out, uint16(len(s.blocks)))
	out = append(out, friends...)
	return append(out, blocks...)
}

func (h *Handler) presenceFriends(ctx context.Context, body []byte, usersHeader string) ([]byte, error) {
	users, err := ticketUsers(usersHeader)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var out []byte
	count := 0
	for len(body) > 0 {
		if len(body) < presenceHeader {
			return nil, deny(400, "short presence header")
		}
		size := uint64(binary.LittleEndian.Uint32(body[4:])) + presenceHeader
		if size > uint64(len(body)) || size < presenceHeader {
			return nil, deny(400, "invalid presence length")
		}
		e := body[:int(size)]
		body = body[int(size):]
		// XRL opcode 0xF logoff: a ticket-bound XUID, no response schema.
		if binary.LittleEndian.Uint32(e) == 0x400 {
			if len(e) != 40 {
				return nil, deny(400, "invalid logoff length")
			}
			if users[binary.LittleEndian.Uint64(e[32:])] == "" {
				return nil, deny(403, "logoff user is not on tunnel ticket")
			}
			count++
			if count > 4 {
				return nil, deny(400, "too many presence entries")
			}
			continue
		}
		if t := binary.LittleEndian.Uint32(e); t != 0x401 {
			count++
			if count > 16 {
				return nil, deny(400, "too many presence entries")
			}
			reply, ok, err := h.other(ctx, e, users)
			if err != nil {
				return nil, err
			}
			if !ok {
				h.log().Warn("unsupported xpresence message", "type", t, "len", len(e))
				return nil, deny(400, "unsupported presence request")
			}
			out = append(out, reply...)
			continue
		}
		if len(e) < 0x99 {
			return nil, deny(400, "short presence alive")
		}
		if binary.LittleEndian.Uint32(e) != 0x401 || int(binary.LittleEndian.Uint16(e[0x28:]))+0x98 != len(e) {
			return nil, deny(400, "unsupported presence request")
		}
		account := users[binary.LittleEndian.Uint64(e[0x20:])]
		if account == "" {
			return nil, deny(403, "presence user is not on tunnel ticket")
		}
		count++
		if count > 4 {
			return nil, deny(400, "too many presence entries")
		}
		snapshot, err := h.friendSnapshot(ctx, account)
		if err != nil {
			h.log().Error("social snapshot failed", "err", err)
			return nil, deny(503, "social service unavailable")
		}
		if h.hasMail(ctx, binary.LittleEndian.Uint64(e[0x20:])) {
			snapshot.hr = hrNewMail
		}
		out = append(out, snapshot.reply(e)...)
	}
	if count == 0 {
		return nil, deny(400, "empty presence request")
	}
	return out, nil
}
