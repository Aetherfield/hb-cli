package zyese

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"
)

func TestWordsComeFromTheGlyphsNeverTheDecoy(t *testing.T) {
	// "cat" with a shift of 3: (v-1-3+52)%26 = letter index
	e := Enemy{Glyphs: []int{1 + 2 + 3, 1 + 0 + 3, 0, 1 + 19 + 3}, GlyphShift: 3, TypedLength: 2}
	if got := e.Word(); got != "ca t" {
		t.Fatalf("word %q", got)
	}
	if d, l := e.Progress(); d != "ca" || l != " t" {
		t.Fatalf("progress %q %q", d, l)
	}
	// a shift bigger than the value wraps round, not below 'a'
	if got := (Enemy{Glyphs: []int{1}, GlyphShift: 5}).Word(); got != "v" {
		t.Fatalf("wrap %q", got)
	}
	zh := Enemy{UnicodeGlyphs: []int{'飞' + 7, '机' + 7}, GlyphShift: 7, TypedLength: 1}
	if d, l := zh.Progress(); d != "飞" || l != "机" {
		t.Fatalf("zh %q %q", d, l)
	}
	ja := Enemy{UnicodeGlyphs: []int{'さ', 'く', 'ら'}, Typed: "さ", TypedLength: 3}
	if d, l := ja.Progress(); d != "さ" || l != "くら" {
		t.Fatalf("ja uses the server's typed kana, not the romaji count: %q %q", d, l)
	}
	if (Enemy{}).Word() != "" {
		t.Fatal("no glyphs: nothing, never the decoy word")
	}
	var withDecoy Enemy
	_ = json.Unmarshal([]byte(`{"word":"decoy","glyphs":[2]}`), &withDecoy)
	if withDecoy.Word() != "b" {
		t.Fatalf("the decoy field is ignored: %q", withDecoy.Word())
	}
}

func TestInputsAckResendAndGaps(t *testing.T) {
	var in Inputs
	for _, k := range "abcde" {
		in.Add(string(k), 1)
	}
	in.Ack(2)
	if un := in.Unacked(); len(un) != 3 || un[0].Seq != 3 {
		t.Fatalf("unacked %+v", un)
	}
	got, ok := in.From(2) // confirmed ones are kept for a while
	if !ok || len(got) != 4 || got[0].Key != "b" {
		t.Fatalf("from 2: %+v %v", got, ok)
	}
	for i := 0; i < keepRecent+10; i++ {
		in.Add("x", 2)
	}
	in.Ack(in.Next() - 1)
	if _, ok := in.From(1); ok {
		t.Fatal("inputs no longer kept must be reported missing")
	}
	if in.Next() != 6+keepRecent+10 {
		t.Fatalf("numbers never restart: %d", in.Next())
	}
}

func TestErrorsAreReadByTheirShape(t *testing.T) {
	e := parseError(401, http.Header{}, []byte(`{"error":{"message":"Invalid or expired authentication token","type":null,"code":"invalid_token","param":null},"status":401}`))
	if e.Code != "invalid_token" || !strings.Contains(e.Error(), "hb zyese login") {
		t.Fatalf("%+v", e)
	}
	e = parseError(403, http.Header{}, []byte(`{"detail":"This client_id has not been approved or has been disabled. Contact the Zyese administrator."}`))
	if e.Code != "client_not_approved" {
		t.Fatalf("%+v", e)
	}
	e = parseError(400, http.Header{}, []byte(`{"detail":"Something new"}`))
	if e.Code != "unknown_error" || !strings.Contains(e.Error(), "Something new") {
		t.Fatalf("an unknown detail is shown as is: %+v", e)
	}
	h := http.Header{}
	h.Set("Retry-After", "60")
	e = parseError(429, h, []byte(`{"detail":"Too many requests. Please try again later."}`))
	if e.Code != "rate_limited" || e.RetryAfter != time.Minute {
		t.Fatalf("%+v", e)
	}
	if (&APIError{Code: "linuxdo_binding_required"}).Hint() == "" {
		t.Fatal("binding hint")
	}
}

func TestCallbackTakesOnlyTheCurrentAttemptOnce(t *testing.T) {
	cb := &callback{result: make(chan cbResult, 1)}
	cb.expect("good-state")
	call := func(q string) int {
		w := httptest.NewRecorder()
		cb.handle(w, httptest.NewRequest("GET", "/callback?"+q, nil))
		return w.Code
	}
	if call("state=other&code=x") != 400 {
		t.Fatal("a wrong state is refused")
	}
	if call("state=good-state&code=the-code") != 200 {
		t.Fatal("the right one is taken")
	}
	if r := <-cb.result; r.code != "the-code" || r.err != nil {
		t.Fatalf("%+v", r)
	}
	if call("state=good-state&code=again") != 400 {
		t.Fatal("a repeat is refused")
	}
	cb2 := &callback{result: make(chan cbResult, 1)}
	cb2.expect("s")
	w := httptest.NewRecorder()
	cb2.handle(w, httptest.NewRequest("GET", "/callback?state=s&error=access_denied", nil))
	if r := <-cb2.result; r.err != ErrCancelled {
		t.Fatalf("cancel: %+v", r)
	}
}

// ---- a fake game server ----

type fakeServer struct {
	t        *testing.T
	srv      *httptest.Server
	mu       sync.Mutex
	frames   []map[string]any // what the client sent on the socket (not heartbeats)
	beats    int
	probeAck map[string]any
	auths    []string // Authorization headers of the handshakes
	abandon  []map[string]string
	conn     *websocket.Conn
	ready    chan struct{}
	got      chan map[string]any
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{t: t, ready: make(chan struct{}, 4), got: make(chan map[string]any, 256)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /openapi/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-1" {
			w.WriteHeader(401)
			io.WriteString(w, `{"error":{"message":"x","code":"invalid_token"}}`)
			return
		}
		io.WriteString(w, `{"runId":"r1","runToken":"run-token-1","livePath":"/openapi/v1/live/r1","rulesVersion":3}`)
	})
	mux.HandleFunc("POST /openapi/v1/runs/r1/abandon", func(w http.ResponseWriter, r *http.Request) {
		var b map[string]string
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.mu.Lock()
		f.abandon = append(f.abandon, b)
		f.mu.Unlock()
		io.WriteString(w, `{}`)
	})
	mux.HandleFunc("GET /openapi/v1/runs/r1", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"runId":"r1","result":{"score":120,"rank":5}}`)
	})
	mux.HandleFunc("/openapi/v1/live/r1", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.mu.Unlock()
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		_, first, err := c.Read(ctx)
		if err != nil {
			return
		}
		var auth map[string]any
		_ = json.Unmarshal(first, &auth)
		if auth["type"] != "auth" || auth["token"] != "run-token-1" || auth["transportVersion"] != float64(3) || auth["inputTelemetryVersion"] != float64(0) {
			c.Close(4001, "bad first frame")
			return
		}
		f.mu.Lock()
		f.conn = c
		f.mu.Unlock()
		f.send(`{"type":"ready","serverTime":1,"state":{"status":"playing","inputMode":"en","score":0,"ack":0,"activeTargetId":null,"enemies":[{"id":1,"x":0.5,"y":0.1,"glyphs":[3,1,20],"glyphShift":0,"typedLength":0}]},"events":null,"networkProbe":{"id":7,"serverSentAtUnixMs":1794913594275.5}}`)
		f.ready <- struct{}{}
		for {
			_, b, err := c.Read(ctx)
			if err != nil {
				return
			}
			var m map[string]any
			_ = json.Unmarshal(b, &m)
			if m["type"] == "heartbeat" {
				f.mu.Lock()
				f.beats++
				if a, ok := m["networkProbeAck"].(map[string]any); ok {
					f.probeAck = a
				}
				f.mu.Unlock()
				continue
			}
			f.mu.Lock()
			f.frames = append(f.frames, m)
			f.mu.Unlock()
			f.got <- m
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) send(s string) {
	f.mu.Lock()
	c := f.conn
	f.mu.Unlock()
	if err := c.Write(context.Background(), websocket.MessageText, []byte(s)); err != nil {
		f.t.Fatalf("send: %v", err)
	}
}

func (f *fakeServer) next() map[string]any {
	select {
	case m := <-f.got:
		return m
	case <-time.After(3 * time.Second):
		f.t.Fatal("no frame from the client")
		return nil
	}
}

// drive runs a command and feeds its message back, as the bubbletea runtime does.
func drive(m *Model, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	_, next := m.Update(msg)
	return next
}

func typeKeys(m *Model, s string) {
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func TestARunEndToEnd(t *testing.T) {
	f := newFakeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cl := &Client{Base: f.srv.URL + "/openapi/v1", HTTP: f.srv.Client(),
		Token: func(context.Context) (string, error) { return "access-1", nil }}
	m := NewModel(ctx, Game{Client: cl, HTTP: f.srv.Client(), Mode: "en"})

	waitCmd := drive(m, drive(m, m.Init())) // start the run, dial → connected → wait
	<-f.ready
	waitCmd = drive(m, waitCmd) // the ready message
	if !m.ready || m.state == nil || len(m.state.Enemies) != 1 || m.state.Enemies[0].Word() != "cat" {
		t.Fatalf("ready: %v %+v", m.ready, m.state)
	}
	if f.auths[0] != "Bearer access-1" {
		t.Fatalf("the handshake carries the access token: %q", f.auths[0])
	}

	// typing: one key per character, numbered from 1
	typeKeys(m, "ca")
	k1, k2 := f.next(), f.next()
	if k1["type"] != "key" || k1["seq"] != float64(1) || k1["key"] != "c" || k2["seq"] != float64(2) || k2["key"] != "a" {
		t.Fatalf("keys %v %v", k1, k2)
	}
	if _, ok := k1["clientEventAt"]; !ok {
		t.Fatal("each key says when it was typed")
	}
	// an input method commits two characters: two keys
	typeKeys(m, "飞机")
	if a, b := f.next(), f.next(); a["key"] != "飞" || b["key"] != "机" || b["seq"] != float64(4) {
		t.Fatalf("ime %v %v", a, b)
	}
	// a paste is refused: nothing goes out
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("cheat"), Paste: true})
	if !strings.Contains(m.notice, "粘贴") {
		t.Fatalf("paste notice %q", m.notice)
	}

	// the server confirms 2 and asks for 3 again
	f.send(`{"type":"snapshot","state":{"status":"playing","ack":2,"enemies":[]}}`)
	waitCmd = drive(m, waitCmd)
	f.send(`{"type":"input_resync","expectedSeq":3,"state":{"status":"playing","ack":2,"enemies":[]}}`)
	waitCmd = drive(m, waitCmd)
	re := f.next()
	ins, _ := re["inputs"].([]any)
	if re["type"] != "keys" || len(ins) != 2 || ins[0].(map[string]any)["seq"] != float64(3) || ins[0].(map[string]any)["key"] != "飞" {
		t.Fatalf("resend keeps numbers and keys: %v", re)
	}
	// nothing else was sent: in particular not the pasted text
	select {
	case extra := <-f.got:
		t.Fatalf("unexpected frame %v", extra)
	case <-time.After(300 * time.Millisecond):
	}

	// pause and resume
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if p := f.next(); p["type"] != "pause" {
		t.Fatalf("pause %v", p)
	}
	typeKeys(m, "x")
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if p := f.next(); p["type"] != "resume" {
		t.Fatalf("keys while paused are not sent; then resume: %v", p)
	}

	// heartbeats run by themselves
	time.Sleep(1200 * time.Millisecond)
	f.mu.Lock()
	beats := f.beats
	f.mu.Unlock()
	if beats < 2 {
		t.Fatalf("heartbeats: %d", beats)
	}
	f.mu.Lock()
	ack := f.probeAck
	f.mu.Unlock()
	if ack == nil || ack["id"] != float64(7) || ack["serverSentAtUnixMs"] != 1794913594275.5 {
		t.Fatalf("the network probe is answered with a heartbeat: %v", ack)
	}
	recv, _ := ack["clientReceivedAtUnixMs"].(float64)
	sent, _ := ack["clientSentAtUnixMs"].(float64)
	if recv <= 0 || sent < recv {
		t.Fatalf("times: received %v sent %v", recv, sent)
	}

	// game over: the result is read from the server
	f.send(`{"type":"gameover","state":{"status":"over","score":120,"ack":4,"enemies":[]}}`)
	resCmd := drive(m, waitCmd)
	if !m.over {
		t.Fatal("over")
	}
	drive(m, resCmd)
	if m.result == nil || !strings.Contains(m.View(), "服务器结算") {
		t.Fatalf("result view:\n%s", m.View())
	}
}

func TestGivingUpTakesTwoCtrlCAndAbandonsTheRun(t *testing.T) {
	f := newFakeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cl := &Client{Base: f.srv.URL + "/openapi/v1", HTTP: f.srv.Client(),
		Token: func(context.Context) (string, error) { return "access-1", nil }}
	m := NewModel(ctx, Game{Client: cl, HTTP: f.srv.Client(), Mode: "en"})
	waitCmd := drive(m, drive(m, m.Init()))
	<-f.ready
	drive(m, waitCmd)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || !m.quitArmed {
		t.Fatal("the first Ctrl+C only asks")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("the second gives up")
	}
	if _, ok := cmd().(quitMsg); !ok {
		t.Fatal("then quits")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.abandon) != 1 || f.abandon[0]["runToken"] != "run-token-1" || f.abandon[0]["reason"] != "player_exit" {
		t.Fatalf("abandon %v", f.abandon)
	}
}

func TestTheDrawingKeepsWordsAndMarksTheTarget(t *testing.T) {
	m := &Model{g: Game{Mode: "zh"}, width: 60, height: 20, run: Run{RunID: "r"}}
	m.state = &State{Status: "playing", ActiveTargetID: json.RawMessage(`"e2"`), Enemies: []Enemy{
		{ID: json.RawMessage(`"e1"`), X: 0.1, Y: 0.2, UnicodeGlyphs: []int{'打', '字'}},
		{ID: json.RawMessage(`"e2"`), X: 0.9, Y: 0.9, UnicodeGlyphs: []int{'飞', '机'}, TypedLength: 1},
		{ID: json.RawMessage(`3`), X: 0.5, Y: 0.5, UnicodeGlyphs: []int{'隐', '身'}, NameStealth: true},
	}}
	v := m.View()
	for _, want := range []string{"打字", "飞", "机", "??"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "隐身") {
		t.Fatal("a stealthy name stays hidden")
	}
	if !SameID(json.RawMessage(`"7"`), json.RawMessage(`7`)) || SameID(json.RawMessage(`null`), json.RawMessage(`null`)) {
		t.Fatal("SameID")
	}
}

func TestDialBuildsTheSocketAddressFromTheAPIBase(t *testing.T) {
	// only the scheme and host of the base are kept; the live path is the server's
	u, _ := url.Parse("https://api-zyese.luminet.cn/openapi/v1")
	if u.Host != "api-zyese.luminet.cn" {
		t.Fatal(u)
	}
	if _, err := Dial(context.Background(), http.DefaultClient, "ftp://x/openapi/v1", "/live", "a", "r", nil); err == nil {
		t.Fatal("a non-http base is refused")
	}
}

// A frame must stay small: one escape code per blank cell made the terminal
// lag and keys arrive in bursts (a run was flagged as automated input).
func TestAFrameIsSmall(t *testing.T) {
	m := &Model{g: Game{Mode: "en"}, width: 200, height: 50, run: Run{RunID: "r"}}
	st := &State{Status: "playing", ActiveTargetID: json.RawMessage(`"e3"`)}
	for i := 0; i < 15; i++ {
		st.Enemies = append(st.Enemies, Enemy{ID: json.RawMessage(`"e` + string(rune('a'+i)) + `"`), X: float64(i) / 15, Y: float64(i%7) / 7, Glyphs: []int{3, 1, 20, 5, 9, 12}, TypedLength: i % 4})
	}
	m.state = st
	if n := len(m.View()); n > 12000 {
		t.Fatalf("a 200×50 frame with 15 planes is %d bytes", n)
	}
}

func TestAClockBehindTheServerIsSaid(t *testing.T) {
	if clockWarning(1794914308349, 1794914307555) == "" {
		t.Fatal("arriving 0.8 s before it was sent: the clock is behind")
	}
	if clockWarning(1794914308349, 1794914308420) != "" {
		t.Fatal("a normal delay is no warning")
	}
	if clockWarning(0, 5) != "" {
		t.Fatal("no server time, no warning")
	}
}

func TestGivingUpIsNotTakenForADrop(t *testing.T) {
	m := &Model{run: Run{RunID: "r"}, quitting: true, ready: true}
	m.Update(liveEndMsg{nil, context.Canceled})
	if m.err != nil || strings.Contains(m.notice, "context") {
		t.Fatalf("err=%v notice=%q", m.err, m.notice)
	}
}

func TestTheSpecsOtherErrorsAndModes(t *testing.T) {
	for detail, code := range map[string]string{
		"This hell mode is locked. Check GET /openapi/v1/profile/hell-attempts.":                              "hell_locked",
		"No hell mode attempts remain today. Check GET /openapi/v1/profile/hell-attempts for the reset time.": "hell_no_attempts",
		"This game session has expired.":                    "session_expired",
		"The requested leaderboard scope is not supported.": "scope_unsupported",
	} {
		b, _ := json.Marshal(map[string]string{"detail": detail})
		e := parseError(400, http.Header{}, b)
		if e.Code != code || e.Hint() == "" || strings.Contains(e.Error(), "HTTP 400") {
			t.Errorf("%q -> %+v", detail, e)
		}
	}
	for _, m := range []string{"en", "zh", "ja", "hell-en", "hell-zh"} {
		if checkMode(m) != nil {
			t.Errorf("%s is a mode", m)
		}
	}
	if checkMode("roguelike") == nil {
		t.Error("hb does not play roguelike")
	}
	cl := &Client{Token: func(context.Context) (string, error) { return "", nil }}
	if _, err := cl.Leaderboard(context.Background(), "event", "ja"); err == nil || !strings.Contains(err.Error(), "活动榜") {
		t.Fatalf("the event board is en and zh only: %v", err)
	}
	if !IsHell("hell-zh") || IsHell("zh") {
		t.Fatal("IsHell")
	}
}

func TestTheResultIsWrittenInChinese(t *testing.T) {
	s := resultLines(map[string]any{"score": 47450.0, "wave": 17.0, "kills": 134.0, "longestStreak": 77.0, "accuracy": 77.0, "moderationStatus": "visible", "newField": true})
	for _, want := range []string{"分数  47450", "最长连击  77", "准确率  77%", "上榜状态  正常上榜", "newField  true"} {
		if !strings.Contains(stripANSI(s), want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if !strings.Contains(stripANSI(resultLines(map[string]any{"moderationStatus": "something"})), "something") {
		t.Error("an unknown status is shown as it comes")
	}
	if finishReason("collision") != "被敌机撞毁（collision）" || finishReason("other") != "other" {
		t.Fatal(finishReason("collision"))
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			esc = true
		case esc && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'):
			esc = false
		case !esc:
			b.WriteRune(r)
		}
	}
	return b.String()
}

type flakyTransport struct{ fails, calls int }

func (f *flakyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls++
	if f.calls <= f.fails {
		return nil, io.ErrUnexpectedEOF
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"event":null}`)), Header: http.Header{}}, nil
}

func TestReadsAreTriedAgainButStartsAreNot(t *testing.T) {
	tok := func(context.Context) (string, error) { return "a", nil }
	ft := &flakyTransport{fails: 2}
	cl := &Client{Base: "https://x", HTTP: &http.Client{Transport: ft}, Token: tok}
	if _, err := cl.ActiveEvent(context.Background()); err != nil || ft.calls != 3 {
		t.Fatalf("a GET is tried three times at most: %v calls=%d", err, ft.calls)
	}
	ft = &flakyTransport{fails: 1}
	cl.HTTP = &http.Client{Transport: ft}
	if _, err := cl.StartRun(context.Background(), "en"); err == nil || ft.calls != 1 {
		t.Fatalf("a POST is never repeated: %v calls=%d", err, ft.calls)
	}
}
