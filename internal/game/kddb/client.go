package kddb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Error is the game's `{message}` answer on a non-2xx status.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("kddb: HTTP %d: %s", e.Status, e.Message) }

// IdentityFunc returns a lafish-signed identity token (ES256) for the packet's
// page, bound to nonce. hb implements it with POST /activities/:id/page-identity.
type IdentityFunc func(ctx context.Context, nonce string) (token string, err error)

// Client talks to one packet's game on the game site. The ticket it gets from
// /hello lasts two hours; on a 401 the client signs in again once.
type Client struct {
	Site     string // game site origin, e.g. https://hbweb-kddb.lafi.sh
	Hongbao  string // the packet id on lafish
	HTTP     *http.Client
	Identity IdentityFunc

	mu       sync.Mutex
	ticket   string
	ticketAt time.Time
	UID      int64
}

func New(site, hongbao string, identity IdentityFunc) *Client {
	return &Client{
		Site: strings.TrimRight(site, "/"), Hongbao: hongbao, Identity: identity,
		HTTP: &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *Client) endpoint(path string, extra url.Values) string {
	q := url.Values{"hb": {c.Hongbao}}
	for k, v := range extra {
		q[k] = v
	}
	return c.Site + "/v3/api/" + path + "?" + q.Encode()
}

func (c *Client) raw(ctx context.Context, method, path string, extra url.Values, body any, authorization string, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path, extra), rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("kddb: cannot reach the game site: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &m)
		if m.Message == "" {
			m.Message = strings.TrimSpace(string(data))
		}
		return &Error{Status: resp.StatusCode, Message: m.Message}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// Hello signs in: nonce -> identity token -> ticket. It returns the state.
func (c *Client) Hello(ctx context.Context) (State, error) {
	var n struct {
		Nonce string `json:"nonce"`
	}
	if err := c.raw(ctx, "GET", "nonce", nil, nil, "", &n); err != nil {
		return State{}, err
	}
	if c.Identity == nil {
		return State{}, errors.New("kddb: no identity source")
	}
	token, err := c.Identity(ctx, n.Nonce)
	if err != nil {
		return State{}, err
	}
	var a struct {
		State
		Ticket string `json:"ticket"`
		UID    int64  `json:"uid"`
	}
	if err := c.raw(ctx, "POST", "hello", nil, nil, "Bearer "+token, &a); err != nil {
		return State{}, err
	}
	c.mu.Lock()
	c.ticket, c.ticketAt, c.UID = a.Ticket, time.Now(), a.UID
	c.mu.Unlock()
	return a.State, nil
}

// call sends a ticketed request, signing in first or again as needed.
func (c *Client) call(ctx context.Context, method, path string, extra url.Values, body, out any) error {
	c.mu.Lock()
	t := c.ticket
	c.mu.Unlock()
	if t == "" {
		if _, err := c.Hello(ctx); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		c.mu.Lock()
		t = c.ticket
		c.mu.Unlock()
		err := c.raw(ctx, method, path, extra, body, "Ticket "+t, out)
		var ge *Error
		if errors.As(err, &ge) && ge.Status == http.StatusUnauthorized && attempt == 0 {
			if _, herr := c.Hello(ctx); herr != nil {
				return herr
			}
			continue
		}
		return err
	}
}

func (c *Client) State(ctx context.Context) (State, error) {
	var s State
	return s, c.call(ctx, "GET", "state", nil, nil, &s)
}

func (c *Client) Dig(ctx context.Context, cardID, index int) (DigAnswer, error) {
	var a DigAnswer
	err := c.call(ctx, "POST", "dig", nil, map[string]any{"cardId": cardID, "index": index, "v": 2}, &a)
	return a, err
}

// Use uses an item (docs/V3_ITEMS.md §4 in kddb). cardID/index are -1 and
// choice "" when the item takes none.
func (c *Client) Use(ctx context.Context, item string, cardID, index int, choice string) (UseAnswer, error) {
	body := map[string]any{"item": item}
	if cardID >= 0 {
		body["cardId"] = cardID
	}
	if index >= 0 {
		body["index"] = index
	}
	if choice != "" {
		body["choice"] = choice
	}
	var a UseAnswer
	err := c.call(ctx, "POST", "use", nil, body, &a)
	return a, err
}

// Summary is the 收益卡: your dig log and the top players.
func (c *Client) Summary(ctx context.Context) (Summary, error) {
	var s Summary
	return s, c.call(ctx, "GET", "summary", nil, nil, &s)
}

// MyUID is the player id the game site gave us at sign-in.
func (c *Client) MyUID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.UID
}

func (c *Client) Claim(ctx context.Context) (ClaimAnswer, error) {
	var a ClaimAnswer
	err := c.call(ctx, "POST", "claim", nil, struct{}{}, &a)
	return a, err
}

// Live connects the push channel and calls onMsg for each message until ctx
// ends or the socket closes (the caller reconnects and calls State to catch up).
func (c *Client) Live(ctx context.Context, onMsg func(Live)) error {
	c.mu.Lock()
	t := c.ticket
	c.mu.Unlock()
	if t == "" {
		if _, err := c.Hello(ctx); err != nil {
			return err
		}
		c.mu.Lock()
		t = c.ticket
		c.mu.Unlock()
	}
	u, _ := url.Parse(c.endpoint("live", url.Values{"ticket": {t}, "v": {"2"}}))
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	conn, _, err := websocket.Dial(ctx, u.String(), nil)
	if err != nil {
		return fmt.Errorf("kddb live: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		for _, m := range ParseLive(data) {
			onMsg(m)
		}
	}
}

// ParseLive splits one frame: v2 sends an array, v1 a single object.
func ParseLive(data []byte) []Live {
	data = bytes.TrimSpace(data)
	var raws []json.RawMessage
	if len(data) > 0 && data[0] == '[' {
		if json.Unmarshal(data, &raws) != nil {
			return nil
		}
	} else {
		raws = []json.RawMessage{data}
	}
	var out []Live
	for _, r := range raws {
		var m Live
		if json.Unmarshal(r, &m) == nil && m.Type != "" {
			m.Raw = r
			out = append(out, m)
		}
	}
	return out
}
