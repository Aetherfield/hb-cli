package zyese

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ---- what the server sends ----

// State is the server's snapshot of a run (ready, snapshot, input_resync).
// Fields the client does not use are ignored; numbers are kept loose.
type State struct {
	Status         string          `json:"status"`
	InputMode      string          `json:"inputMode"`
	Score          float64         `json:"score"`
	Wave           float64         `json:"wave"`
	Streak         float64         `json:"streak"`
	LongestStreak  float64         `json:"longestStreak"`
	Kills          float64         `json:"kills"`
	Hits           float64         `json:"hits"`
	Misses         float64         `json:"misses"`
	Elapsed        float64         `json:"elapsed"`
	ActiveTargetID json.RawMessage `json:"activeTargetId"`
	Ack            int             `json:"ack"`
	PersistedAck   int             `json:"persistedAck"`
	Ranked         bool            `json:"ranked"`
	FinishReason   *string         `json:"finishReason"`
	Enemies        []Enemy         `json:"enemies"`
	Result         json.RawMessage `json:"result"`
	Shield         any             `json:"shield"`
	FocusCharges   any             `json:"focusCharges"`
	ScoreBoostMs   any             `json:"scoreBoostMs"`
	TimeStop       any             `json:"timeStop"`
}

// Enemy is one plane. X and Y are on a 480×720 field, normalised to 0..1.
type Enemy struct {
	ID            json.RawMessage `json:"id"`
	X             float64         `json:"x"`
	Y             float64         `json:"y"`
	Type          string          `json:"type"`
	Dying         bool            `json:"dying"`
	NameStealth   bool            `json:"nameStealth"`
	Glyphs        []int           `json:"glyphs"`
	UnicodeGlyphs []int           `json:"unicodeGlyphs"`
	GlyphShift    int             `json:"glyphShift"`
	TypedLength   int             `json:"typedLength"`
	Typed         string          `json:"typed"`
}

// Word is the plane's word, for showing only. transportVersion 3's "word"
// field is a decoy and never used: the word comes from the glyphs.
func (e Enemy) Word() string {
	if e.UnicodeGlyphs != nil {
		var b strings.Builder
		for _, v := range e.UnicodeGlyphs {
			b.WriteRune(rune(v - e.GlyphShift))
		}
		return b.String()
	}
	if e.Glyphs != nil {
		var b strings.Builder
		for _, v := range e.Glyphs {
			if v == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteByte(byte((((v-1-e.GlyphShift+52)%26)+26)%26 + 'a'))
		}
		return b.String()
	}
	return ""
}

// Progress splits the word into what is typed and what is left, by runes
// (Japanese shows the server's own typed kana when it sends it).
func (e Enemy) Progress() (done, left string) {
	w := []rune(e.Word())
	if e.Typed != "" {
		return e.Typed, string(w[min(len([]rune(e.Typed)), len(w)):])
	}
	n := max(0, min(e.TypedLength, len(w)))
	return string(w[:n]), string(w[n:])
}

// SameID compares an enemy id with activeTargetId, whatever JSON type they are.
func SameID(a, b json.RawMessage) bool {
	a, b = bytes.Trim(bytes.TrimSpace(a), `"`), bytes.Trim(bytes.TrimSpace(b), `"`)
	return len(a) > 0 && !bytes.Equal(a, []byte("null")) && bytes.Equal(a, b)
}

// Msg is one message from the server.
type Msg struct {
	Type        string          `json:"type"`
	State       *State          `json:"state"`
	ExpectedSeq int             `json:"expectedSeq"`
	Result      json.RawMessage `json:"result"`
	Reason      string          `json:"reason"`
	Message     string          `json:"message"`
	// NetworkProbe is the server measuring the connection; the next heartbeat answers it.
	NetworkProbe *struct {
		ID                 int64   `json:"id"`
		ServerSentAtUnixMs float64 `json:"serverSentAtUnixMs"`
	} `json:"networkProbe"`
	ServerTime float64 `json:"serverTime"`
	// ReceivedAt is when it arrived here (wall clock, ms).
	ReceivedAt float64         `json:"-"`
	Raw        json.RawMessage `json:"-"`
}

// ---- the keys typed ----

// Input is one character typed, with its sequence number and when (ms on a
// monotonic clock).
type Input struct {
	Seq int     `json:"seq"`
	Key string  `json:"key"`
	At  float64 `json:"clientEventAt,omitempty"`
	// input telemetry version 1: where the key came from (see hook.go)
	EventID int    `json:"eventId,omitempty"`
	Source  string `json:"inputSource,omitempty"`
	Trusted *bool  `json:"trusted,omitempty"`
	Code    string `json:"code,omitempty"`
	Repeat  *bool  `json:"repeat,omitempty"`
}

// Inputs numbers what the person types and keeps what the server has not
// confirmed (and some it has), so it can be sent again after a gap or a
// reconnect. It never makes up a key.
type Inputs struct {
	mu      sync.Mutex
	next    int
	unacked []Input
	recent  []Input // confirmed, the last few
	ack     int
}

const keepRecent = 128

// Add numbers a key. t is its telemetry, nil for version 0.
func (in *Inputs) Add(key string, at float64, t *Telemetry) Input {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.next++
	i := Input{Seq: in.next, Key: key, At: at}
	if t != nil {
		tr, rp := t.Trusted, t.Repeat
		i.EventID, i.Source, i.Trusted, i.Code, i.Repeat, i.At = t.EventID, t.Source, &tr, t.Code, &rp, t.EventAt
	}
	in.unacked = append(in.unacked, i)
	return i
}

// Ack drops what the server has processed up to n (keeping a few).
func (in *Inputs) Ack(n int) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if n <= in.ack {
		return
	}
	in.ack = n
	k := 0
	for k < len(in.unacked) && in.unacked[k].Seq <= n {
		k++
	}
	in.recent = append(in.recent, in.unacked[:k]...)
	in.unacked = append([]Input(nil), in.unacked[k:]...)
	if len(in.recent) > keepRecent {
		in.recent = append([]Input(nil), in.recent[len(in.recent)-keepRecent:]...)
	}
}

// From is every input from seq on, in order, as first typed; ok is false
// when some of them are no longer kept.
func (in *Inputs) From(seq int) (out []Input, ok bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	all := append(append([]Input(nil), in.recent...), in.unacked...)
	for _, i := range all {
		if i.Seq >= seq {
			out = append(out, i)
		}
	}
	if seq > in.next {
		return nil, true
	}
	return out, len(out) > 0 && out[0].Seq == seq
}

// Unacked is what the server has not confirmed yet.
func (in *Inputs) Unacked() []Input {
	in.mu.Lock()
	defer in.mu.Unlock()
	return append([]Input(nil), in.unacked...)
}

// Next is the sequence number the next key will get.
func (in *Inputs) Next() int {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.next + 1
}

// ---- the connection ----

// Conn is a run's live connection.
type Conn struct {
	ws     *websocket.Conn
	wmu    sync.Mutex
	out    chan []byte // frames waiting to go, in order (the screen never waits on the network)
	pmu    sync.Mutex
	probe  *probeAck // the latest network probe, answered with the next heartbeat
	Msgs   chan Msg
	err    error
	done   chan struct{}
	cancel context.CancelFunc
	trace  io.Writer
}

// probeAck answers the server's network probe, as the web page does: the
// probe's id and send time, when it arrived here, and (added at sending) when
// the answer left. All on the wall clock, in ms.
type probeAck struct {
	ID                     int64   `json:"id"`
	ServerSentAtUnixMs     float64 `json:"serverSentAtUnixMs"`
	ClientReceivedAtUnixMs float64 `json:"clientReceivedAtUnixMs"`
	ClientSentAtUnixMs     float64 `json:"clientSentAtUnixMs,omitempty"`
}

func unixMs() float64 { return float64(time.Now().UnixNano()) / 1e6 }

// Close codes the server uses.
const (
	CloseAuth    = 4001 // access token expired, the app lost its permission, or a bad first frame
	CloseExpired = 4009 // the run expired: it cannot go on
)

// Dial opens the live connection with the access token in the handshake and
// sends the first frame (the run token), then reads messages into Msgs until
// the connection ends; Err says why. trace, if not nil, gets every message
// the server sent (never what is sent to it, which carries the run token).
func Dial(ctx context.Context, hc *http.Client, apiBase, livePath, access, runToken string, telemetry int, trace io.Writer) (*Conn, error) {
	origin := apiBase
	if i := strings.Index(origin, "/openapi/"); i >= 0 {
		origin = origin[:i]
	}
	var endpoint string
	switch {
	case strings.HasPrefix(origin, "https://"):
		endpoint = "wss://" + strings.TrimPrefix(origin, "https://") + livePath
	case strings.HasPrefix(origin, "http://"): // a local test server only
		endpoint = "ws://" + strings.TrimPrefix(origin, "http://") + livePath
	default:
		return nil, fmt.Errorf("bad API address %q", apiBase)
	}
	dctx, dcancel := context.WithTimeout(ctx, 15*time.Second)
	defer dcancel()
	ws, _, err := websocket.Dial(dctx, endpoint, &websocket.DialOptions{
		HTTPClient: hc,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + access}},
	})
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(1 << 20)
	cctx, cancel := context.WithCancel(ctx)
	c := &Conn{ws: ws, Msgs: make(chan Msg, 64), out: make(chan []byte, 512), done: make(chan struct{}), cancel: cancel, trace: trace}
	first := map[string]any{"type": "auth", "token": runToken, "transportVersion": 3, "inputTelemetryVersion": telemetry}
	if err := c.send(cctx, first); err != nil {
		cancel()
		ws.CloseNow()
		return nil, err
	}
	go c.read(cctx)
	go c.write(cctx)
	go c.heartbeat(cctx)
	return c, nil
}

func (c *Conn) read(ctx context.Context) {
	defer close(c.Msgs)
	defer close(c.done)
	for {
		_, b, err := c.ws.Read(ctx)
		if err != nil {
			c.err = err
			c.cancel()
			return
		}
		if c.trace != nil {
			fmt.Fprintf(c.trace, "%s\t%s\n", time.Now().Format(time.RFC3339Nano), b)
		}
		got := unixMs()
		var m Msg
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if p := m.NetworkProbe; p != nil && p.ID > 0 && p.ServerSentAtUnixMs > 0 {
			c.pmu.Lock()
			c.probe = &probeAck{ID: p.ID, ServerSentAtUnixMs: p.ServerSentAtUnixMs, ClientReceivedAtUnixMs: got}
			c.pmu.Unlock()
		}
		m.Raw, m.ReceivedAt = b, got
		select {
		case c.Msgs <- m:
		case <-ctx.Done():
			return
		}
	}
}

// heartbeat keeps the run alive (the server's clock advances on it), every 500 ms.
func (c *Conn) heartbeat(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			beat := map[string]any{"type": "heartbeat"}
			c.pmu.Lock()
			if p := c.probe; p != nil {
				a := *p
				a.ClientSentAtUnixMs = unixMs()
				beat["networkProbeAck"] = a
				c.probe = nil
			}
			c.pmu.Unlock()
			if c.queue(beat) != nil {
				return
			}
		}
	}
}

// queue puts a frame in line to be sent; it never blocks the caller.
func (c *Conn) queue(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > 1024 {
		return errors.New("frame over 1024 bytes")
	}
	select {
	case c.out <- b:
		return nil
	default:
		return errors.New("发送队列满了，连接可能卡住了")
	}
}

// write sends the queued frames, one after another.
func (c *Conn) write(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-c.out:
			c.wmu.Lock()
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := c.ws.Write(wctx, websocket.MessageText, b)
			cancel()
			c.wmu.Unlock()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}

// send writes a frame now (the first frame, before anything is queued).
func (c *Conn) send(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > 1024 {
		return errors.New("frame over 1024 bytes")
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, b)
}

// Key sends one typed character.
func (c *Conn) Key(ctx context.Context, i Input) error {
	return c.queue(struct {
		Type string `json:"type"`
		Input
	}{"key", i})
}

// KeyUp tells the server a key it was told about was released (version 1).
func (c *Conn) KeyUp(u KeyUp) error {
	trusted := u.Trusted
	return c.queue(map[string]any{"type": "key_up", "eventId": u.EventID, "key": u.Key, "inputSource": "physical",
		"trusted": trusted, "clientEventAt": u.EventAt, "code": u.Code})
}

// Resend sends inputs again, in order, at most 16 to a frame, with their
// original numbers, keys and times.
func (c *Conn) Resend(ctx context.Context, ins []Input) error {
	for len(ins) > 0 {
		n := min(16, len(ins))
		if err := c.queue(map[string]any{"type": "keys", "inputs": ins[:n]}); err != nil {
			return err
		}
		ins = ins[n:]
	}
	return nil
}

// ResyncUnavailable tells the server the inputs it asked for are gone.
func (c *Conn) ResyncUnavailable(ctx context.Context, expected, next int) error {
	return c.queue(map[string]any{"type": "input_resync_unavailable", "expectedSeq": expected, "nextSeq": next})
}

// Pause and Resume are the person's own pause.
func (c *Conn) Pause(ctx context.Context) error {
	return c.queue(map[string]string{"type": "pause"})
}
func (c *Conn) Resume(ctx context.Context) error {
	return c.queue(map[string]string{"type": "resume"})
}

// Close ends the connection.
func (c *Conn) Close() {
	c.cancel()
	_ = c.ws.Close(websocket.StatusNormalClosure, "")
}

// Err is why the connection ended (after Msgs is closed).
func (c *Conn) Err() error {
	<-c.done
	return c.err
}

// CloseCode is the server's close code, or -1.
func CloseCode(err error) int {
	return int(websocket.CloseStatus(err))
}
