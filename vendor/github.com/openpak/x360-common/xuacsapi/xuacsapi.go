// Package xuacsapi is the client for x360-xuacs's internal API: the identity
// data (online XUIDs, gamertags, console profiles, the WLID CA) that the other
// Xbox 360 services read from the account service that owns it.
package xuacsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// KeyHeader carries the shared internal key.
const KeyHeader = "X-Internal-Key"

// Client calls the internal API at URL (e.g. http://openpak-x360-xuacs:10006).
type Client struct {
	URL, Key string
	HTTP     *http.Client
}

// StatusError is a non-200 answer; Status is the HTTP status.
type StatusError struct {
	Status int
	Msg    string
}

func (e *StatusError) Error() string { return fmt.Sprintf("xuacs internal API: %d %s", e.Status, e.Msg) }

func (c *Client) call(ctx context.Context, path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(KeyHeader, c.Key)
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.NewDecoder(resp.Body).Decode(&e)
		return &StatusError{resp.StatusCode, e.Error}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func parseXUID(s string) (uint64, error) { return strconv.ParseUint(s, 16, 64) }

// Identity is an account's online XUID (allocated on first use) and gamertag.
func (c *Client) Identity(ctx context.Context, account string) (xuid uint64, gamertag string, err error) {
	var out struct {
		Online   string `json:"online_xuid"`
		Gamertag string `json:"gamertag"`
	}
	if err = c.call(ctx, "/internal/v1/identity", map[string]string{"account": account}, &out); err != nil {
		return 0, "", err
	}
	xuid, err = parseXUID(out.Online)
	return xuid, out.Gamertag, err
}

// OnlineXUID is an account's online XUID alone (allocated on first use), for
// accounts that need no gamertag, such as blocked users.
func (c *Client) OnlineXUID(ctx context.Context, account string) (uint64, error) {
	var out struct {
		Online string `json:"online_xuid"`
	}
	if err := c.call(ctx, "/internal/v1/online-xuid", map[string]string{"account": account}, &out); err != nil {
		return 0, err
	}
	return parseXUID(out.Online)
}

// AccountByXUID is the account behind an online XUID; ok is false when none is.
func (c *Client) AccountByXUID(ctx context.Context, xuid uint64) (account string, ok bool, err error) {
	var out struct {
		Account string `json:"account"`
	}
	err = c.call(ctx, "/internal/v1/xuid-account", map[string]string{"online_xuid": fmt.Sprintf("%016x", xuid)}, &out)
	if se, is := err.(*StatusError); is && se.Status == http.StatusNotFound {
		return "", false, nil
	}
	return out.Account, err == nil, err
}

// ConsoleProfile is a profile issued on a console.
type ConsoleProfile struct {
	Account string
	Online  uint64
}

// ConsoleProfiles lists the profiles issued on a console (its 12 digits).
func (c *Client) ConsoleProfiles(ctx context.Context, console string) ([]ConsoleProfile, error) {
	var out struct {
		Profiles []struct {
			Account string `json:"account"`
			Online  string `json:"online_xuid"`
		} `json:"profiles"`
	}
	if err := c.call(ctx, "/internal/v1/console-profiles", map[string]string{"console": console}, &out); err != nil {
		return nil, err
	}
	var ps []ConsoleProfile
	for _, p := range out.Profiles {
		x, err := parseXUID(p.Online)
		if err != nil {
			return nil, err
		}
		ps = append(ps, ConsoleProfile{p.Account, x})
	}
	return ps, nil
}

// WLIDCA is the PEM certificate of the CA that signs the WLID TLS front's leaves.
func (c *Client) WLIDCA(ctx context.Context) ([]byte, error) {
	var out struct {
		PEM string `json:"ca_pem"`
	}
	if err := c.call(ctx, "/internal/v1/wlid-ca", struct{}{}, &out); err != nil {
		return nil, err
	}
	return []byte(out.PEM), nil
}
