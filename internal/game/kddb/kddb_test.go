package kddb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// fakeSite is just enough of the game Worker to test the client against the
// protocol read from kddb's game/worker/index.mjs.
type fakeSite struct {
	srv        *httptest.Server
	hellos     atomic.Int32
	expireNext atomic.Bool
	lastDig    atomic.Value // map[string]any
}

func newFake(t *testing.T) *fakeSite {
	f := &fakeSite{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/api/nonce", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("hb") != "hb1" {
			http.Error(w, `{"message":"no hb"}`, 400)
			return
		}
		w.Write([]byte(`{"nonce":"N0NCE"}`))
	})
	mux.HandleFunc("/v3/api/hello", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer idtoken-for-N0NCE" {
			w.WriteHeader(401)
			w.Write([]byte(`{"message":"认不出你"}`))
			return
		}
		n := f.hellos.Add(1)
		w.Write([]byte(`{"ticket":"T` + string(rune('0'+n)) + `","uid":7,"game":{"seq":5,"left":100,"over":false,
			"cards":[{"id":1,"x":0,"y":0,"size":3,"cells":[null,null,null,null,null,null,null,null,null],"left":{"emerald":3},"finished":false}]},
			"me":{"chances":20,"points":0,"status":"playing"}}`))
	})
	mux.HandleFunc("/v3/api/state", func(w http.ResponseWriter, r *http.Request) {
		if f.expireNext.CompareAndSwap(true, false) {
			w.WriteHeader(401)
			w.Write([]byte(`{"message":"票据过期了"}`))
			return
		}
		if r.Header.Get("Authorization") == "" || r.Header.Get("Authorization")[:7] != "Ticket " {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"game":{"seq":6,"left":99,"cards":[]},"me":{"chances":19,"points":50,"status":"playing"}}`))
	})
	mux.HandleFunc("/v3/api/dig", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.lastDig.Store(body)
		w.Write([]byte(`{"ok":true,"result":{"kind":"emerald","cardId":1,"index":4,"amount":50,"text":"挖到绿宝石"},"me":{"chances":19,"points":50,"status":"playing"},"claimNow":false}`))
	})
	mux.HandleFunc("/v3/api/claim", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"voucher":"kd3.123.7.50.k1.sig","points":50,"me":{"status":"claimed","points":0}}`))
	})
	mux.HandleFunc("/v3/api/live", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ticket") == "" || r.URL.Query().Get("v") != "2" {
			http.Error(w, "bad", 400)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		_ = c.Write(r.Context(), websocket.MessageText, []byte(`[{"type":"cell","seq":7,"cardId":1,"index":2,"cell":{"kind":"tnt","uid":9,"name":"bob","at":1},"left":98,"text":"bob 踩到 TNT"}]`))
		c.Close(websocket.StatusNormalClosure, "bye")
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func identity(ctx context.Context, nonce string) (string, error) { return "idtoken-for-" + nonce, nil }

func TestHelloThenTicketedCalls(t *testing.T) {
	f := newFake(t)
	c := New(f.srv.URL, "hb1", identity)
	st, err := c.Hello(context.Background())
	if err != nil || c.UID != 7 || len(st.Game.Cards) != 1 || st.Me.Chances != 20 {
		t.Fatalf("hello: %+v uid=%d err=%v", st, c.UID, err)
	}
	s2, err := c.State(context.Background())
	if err != nil || s2.Me.Points != 50 || f.hellos.Load() != 1 {
		t.Fatalf("state: %+v hellos=%d err=%v", s2, f.hellos.Load(), err)
	}
}

func TestExpiredTicketSignsInAgainOnce(t *testing.T) {
	f := newFake(t)
	c := New(f.srv.URL, "hb1", identity)
	if _, err := c.Hello(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.expireNext.Store(true)
	if _, err := c.State(context.Background()); err != nil {
		t.Fatalf("should recover from one 401: %v", err)
	}
	if f.hellos.Load() != 2 {
		t.Fatalf("hellos=%d, want 2", f.hellos.Load())
	}
}

func TestDigAndClaim(t *testing.T) {
	f := newFake(t)
	c := New(f.srv.URL, "hb1", identity)
	a, err := c.Dig(context.Background(), 1, 4) // signs in by itself first
	if err != nil || !a.OK || a.Result.Kind != "emerald" || a.Result.Amount != 50 {
		t.Fatalf("dig: %+v %v", a, err)
	}
	body := f.lastDig.Load().(map[string]any)
	if body["cardId"] != float64(1) || body["index"] != float64(4) || body["v"] != float64(2) {
		t.Fatalf("dig body %v", body)
	}
	cl, err := c.Claim(context.Background())
	if err != nil || !cl.OK || cl.Voucher != "kd3.123.7.50.k1.sig" {
		t.Fatalf("claim: %+v %v", cl, err)
	}
}

func TestHelloRejected(t *testing.T) {
	f := newFake(t)
	c := New(f.srv.URL, "hb1", func(context.Context, string) (string, error) { return "wrong", nil })
	_, err := c.Hello(context.Background())
	var ge *Error
	if err == nil || !asErr(err, &ge) || ge.Status != 401 {
		t.Fatalf("err=%v", err)
	}
}

func asErr(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

func TestLiveDeliversMessagesAndApplyMerges(t *testing.T) {
	f := newFake(t)
	c := New(f.srv.URL, "hb1", identity)
	st, err := c.Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got []Live
	_ = c.Live(ctx, func(m Live) { got = append(got, m) }) // returns when the server closes
	if len(got) != 1 || got[0].Type != "cell" || got[0].Cell.Kind != "tnt" {
		t.Fatalf("live: %+v", got)
	}
	// hello said seq 5, this push is seq 7: one message was missed. It is
	// merged anyway, and the caller is told to reload.
	if ok := st.Apply(got[0]); ok {
		t.Fatal("a gap in seq must be reported (inSync=false)")
	}
	if st.Game.Cards[0].Cells[2] == nil || st.Game.Cards[0].Cells[2].Name != "bob" || st.Game.Left != 98 {
		t.Fatalf("apply: %+v", st.Game.Cards[0])
	}
}

func TestParseLiveShapes(t *testing.T) {
	if n := len(ParseLive([]byte(`[{"type":"cell"},{"type":"claim"}]`))); n != 2 {
		t.Fatalf("array: %d", n)
	}
	if n := len(ParseLive([]byte(`{"type":"drain","uids":[1]}`))); n != 1 {
		t.Fatalf("single: %d", n)
	}
	if n := len(ParseLive([]byte(`not json`))); n != 0 {
		t.Fatalf("garbage: %d", n)
	}
}

func TestPutNeverLosesOpenedCells(t *testing.T) {
	g := PublicGame{Cards: []PublicCard{{ID: 1, Cells: []*OpenCell{{Kind: "emerald"}, nil}}}}
	g.put(PublicCard{ID: 1, Cells: []*OpenCell{nil, {Kind: "tnt"}}})
	c := g.Cards[0]
	if c.Cells[0] == nil || c.Cells[1] == nil || c.Opened() != 2 {
		t.Fatalf("%+v", c.Cells)
	}
}
