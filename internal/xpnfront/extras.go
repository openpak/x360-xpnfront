package xpnfront

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openpak/x360-common/pgstate"
)

// hrNewMail is the per-user hr in a 0x2 reply that makes the console fetch its inbox.
const hrNewMail = 0x001512F0

const blobLen = 60 // the per-user presence blob (requests 0x16/0x17)

// Schema is the Postgres schema x360-xpnfront owns inside the x360 database.
const Schema = "x360_xpnfront"

const blobsKey = "blobs"

// State keeps the per-user presence blobs as one document in Postgres; a nil pg
// keeps them in memory only (tests).
type State struct {
	pg   *pgstate.Store
	mu   sync.Mutex
	blob map[string]string // xuid (16 hex) -> 60 bytes in hex
}

// NewMemoryState is a state that forgets on exit (tests).
func NewMemoryState() *State { return &State{blob: map[string]string{}} }

// OpenState opens the Postgres state in schema. When the database holds none
// yet and legacyFile (the old state.json) exists, it is imported once and left
// in place. An empty dsn is an error.
func OpenState(ctx context.Context, dsn, schema, legacyFile string) (*State, error) {
	pg, err := pgstate.Open(ctx, dsn, schema)
	if err != nil {
		return nil, err
	}
	s := &State{pg: pg, blob: map[string]string{}}
	found, err := pg.Load(ctx, blobsKey, &s.blob)
	if err == nil && !found && legacyFile != "" {
		raw, rerr := os.ReadFile(legacyFile)
		switch {
		case rerr == nil:
			if err = json.Unmarshal(raw, &s.blob); err == nil {
				err = pg.Save(ctx, blobsKey, s.blob)
			}
			if err == nil {
				slog.Info("imported state.json into Postgres", "blobs", len(s.blob))
			}
		case !os.IsNotExist(rerr):
			err = rerr
		}
	}
	if err != nil {
		pg.Close()
		return nil, err
	}
	if s.blob == nil {
		s.blob = map[string]string{}
	}
	return s, nil
}

// Close releases the database pool.
func (s *State) Close() {
	if s.pg != nil {
		s.pg.Close()
	}
}

// Set stores b for xuid; anything but 60 bytes clears it.
func (s *State) Set(xuid uint64, b []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := fmt.Sprintf("%016x", xuid)
	if len(b) == blobLen {
		s.blob[k] = hex.EncodeToString(b)
	} else {
		delete(s.blob, k)
	}
	if s.pg == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.pg.Save(ctx, blobsKey, s.blob)
}

// Get returns the stored 60 bytes, or nil.
func (s *State) Get(xuid uint64) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := hex.DecodeString(s.blob[fmt.Sprintf("%016x", xuid)])
	return b
}

// Unreads says how many unread messages a user has.
type Unreads interface {
	Unread(ctx context.Context, xuid uint64) (int, error)
}

// UnreadClient asks x360-xmessaging's internal API. It fails soft: the caller
// treats an error as "no mail" and the error is logged at most once a minute.
type UnreadClient struct {
	URL, Key string
	HTTP     *http.Client
}

func (c *UnreadClient) Unread(ctx context.Context, xuid uint64) (int, error) {
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(c.URL, "/")+"/internal/v1/unread?xuid="+url.QueryEscape(fmt.Sprintf("%016x", xuid)), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Internal-Key", c.Key)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unread: HTTP %d", resp.StatusCode)
	}
	var out struct {
		Unread int `json:"unread"`
	}
	if json.Unmarshal(body, &out) == nil {
		return out.Unread, nil
	}
	return strconv.Atoi(strings.TrimSpace(string(body)))
}

// hasMail is fail-soft: any error means no flag.
func (h *Handler) hasMail(ctx context.Context, xuid uint64) bool {
	if h.Mail == nil {
		return false
	}
	n, err := h.Mail.Unread(ctx, xuid)
	if err != nil {
		h.warnMu.Lock()
		if time.Since(h.warned) > time.Minute {
			h.warned = time.Now()
			h.log().Warn("unread lookup failed, no new-mail flag", "err", err)
		}
		h.warnMu.Unlock()
		return false
	}
	return n > 0
}

// echo is the request header with msg_len set for a reply body of n bytes.
func echo(e []byte, n int) []byte {
	out := append([]byte{}, e[:presenceHeader]...)
	binary.LittleEndian.PutUint32(out[4:], uint32(n))
	return out
}

// other handles the non-presence requests of the xpresence endpoint. ok is false
// for an unknown message type.
func (h *Handler) other(ctx context.Context, e []byte, users map[uint64]string) (out []byte, ok bool, err error) {
	le := binary.LittleEndian
	need := func(min int) error {
		if len(e) < min {
			return deny(400, "short request")
		}
		return nil
	}
	onTicket := func(xuid uint64) (string, error) {
		if a := users[xuid]; a != "" {
			return a, nil
		}
		return "", deny(403, "user is not on tunnel ticket")
	}
	switch le.Uint32(e) {
	case 0x3F0, 0x3F1: // block / unblock: header, u64 owner, u64 target
		if len(e) != 48 {
			return nil, true, deny(400, "invalid block request")
		}
		owner, err := onTicket(le.Uint64(e[0x20:]))
		if err != nil {
			return nil, true, err
		}
		target := le.Uint64(e[0x28:])
		if h.IDs == nil || h.Social == nil {
			return nil, true, deny(503, "social service unavailable")
		}
		account, found, err := h.IDs.AccountByXUID(ctx, target)
		if err != nil {
			return nil, true, deny(503, "account service unavailable")
		}
		if !found {
			return nil, true, deny(404, "unknown target")
		}
		if le.Uint32(e) == 0x3F0 {
			err = h.Social.Block(ctx, owner, account)
		} else {
			err = h.Social.Unblock(ctx, owner, account)
		}
		if err != nil {
			h.log().Error("block change failed", "type", le.Uint32(e), "err", err)
			return nil, true, deny(503, "social service unavailable")
		}
		return nil, true, nil
	// ponytail: invite delivery to the recipient's inbox comes with x360-xmessaging v0.2 (all four invite ids)
	case 0x3F4: // 0x581 invite answer: header, u64 user, u64 inviter, xnkid 8, u16 answer
		if len(e) != 58 {
			return nil, true, deny(400, "invalid invite answer")
		}
		if _, err := onTicket(le.Uint64(e[0x20:])); err != nil {
			return nil, true, err
		}
		h.log().Info("invite answer", "user", le.Uint64(e[0x20:]), "inviter", le.Uint64(e[0x28:]), "answer", le.Uint16(e[0x38:]))
	case 0x3F3: // 0x582 invite revoke: header, u64 user, xnkid 8, u16 n, u64[n]
		if err := need(0x32); err != nil {
			return nil, true, err
		}
		n := int(le.Uint16(e[0x30:]))
		if n < 1 || n > 100 || len(e) != 0x32+8*n {
			return nil, true, deny(400, "invalid invite revoke")
		}
		if _, err := onTicket(le.Uint64(e[0x20:])); err != nil {
			return nil, true, err
		}
		h.log().Info("invite revoke", "user", le.Uint64(e[0x20:]), "targets", n, "first", le.Uint64(e[0x32:]))
	case 0x417: // 0x580 invite to <=100 xuids: header, u64, u64, u32 logon, u32, u16 n, u16, u64[n], ...
		if err := need(0x40); err != nil {
			return nil, true, err
		}
		a, b := le.Uint64(e[0x20:]), le.Uint64(e[0x28:])
		if users[a] == "" && users[b] == "" {
			return nil, true, deny(403, "user is not on tunnel ticket")
		}
		n := int(le.Uint16(e[0x38:]))
		if n > 100 || len(e) < 0x3C+8*n {
			return nil, true, deny(400, "invalid invite")
		}
		first := uint64(0)
		if n > 0 {
			first = le.Uint64(e[0x3C:])
		}
		h.log().Info("invite", "u1", a, "u2", b, "logon", le.Uint32(e[0x30:]), "targets", n, "first", first)
	case 0x416: // 0x207 invite to one xuid: header, u64 user, u64 target, u32, ...
		if err := need(0x34); err != nil {
			return nil, true, err
		}
		if _, err := onTicket(le.Uint64(e[0x20:])); err != nil {
			return nil, true, err
		}
		h.log().Info("invite", "user", le.Uint64(e[0x20:]), "target", le.Uint64(e[0x28:]), "field", le.Uint32(e[0x30:]))
	case 0x42F: // 0x16 set blob: header, u64 user, u16 n, u8[n]
		if err := need(0x2A); err != nil {
			return nil, true, err
		}
		n := int(le.Uint16(e[0x28:]))
		if n > 128 || len(e) != 0x2A+n {
			return nil, true, deny(400, "invalid presence data")
		}
		user := le.Uint64(e[0x20:])
		if _, err := onTicket(user); err != nil {
			return nil, true, err
		}
		if h.State != nil {
			if err := h.State.Set(user, e[0x2A:]); err != nil {
				h.log().Error("presence data save failed", "err", err)
				return nil, true, deny(503, "state unavailable")
			}
		}
		return append(echo(e, 4), 0, 0, 0, 0), true, nil
	case 0x430: // 0x17 get blob: header, u64 user
		if len(e) != 40 {
			return nil, true, deny(400, "invalid presence data request")
		}
		user := le.Uint64(e[0x20:])
		if _, err := onTicket(user); err != nil {
			return nil, true, err
		}
		var b []byte
		if h.State != nil {
			b = h.State.Get(user)
		}
		out = append(echo(e, 6+len(b)), 0, 0, 0, 0)
		out = le.AppendUint16(out, uint16(len(b)))
		return append(out, b...), true, nil
	default:
		return nil, false, nil
	}
	return nil, true, nil
}

// Erase drops the presence blobs of xuids and returns how many existed.
func (s *State) Erase(xuids []uint64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, x := range xuids {
		k := fmt.Sprintf("%016x", x)
		if _, ok := s.blob[k]; ok {
			delete(s.blob, k)
			n++
		}
	}
	if n == 0 || s.pg == nil {
		return n, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return n, s.pg.Save(ctx, blobsKey, s.blob)
}
