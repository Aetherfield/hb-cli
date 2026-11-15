package mcpserver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"hb/internal/auth"
	"hb/internal/game/kddb"
)

type fakeGame struct {
	mu        sync.Mutex
	state     kddb.State
	digs      [][2]int
	dangerAt  map[int]bool
	rejectAt  map[int]bool
	claims    int
	claimOK   bool
	submitErr error
	submitted []string
	stateErr  error
}

func newFakeGame() *fakeGame {
	cells := make([]*kddb.OpenCell, 9)
	return &fakeGame{
		dangerAt: map[int]bool{}, rejectAt: map[int]bool{}, claimOK: true,
		state: kddb.State{
			Game: kddb.PublicGame{Rules: kddb.Rules{DigInterval: 3}, Cards: []kddb.PublicCard{{
				ID: 1, Size: 3, Cells: cells, Fixed: map[string]string{"4": "deco"},
				Left: map[kddb.Kind]int{"emerald": 5, "diamond": 1, "tnt": 2},
			}}},
			Me: kddb.Me{Chances: 8, Points: 120, Status: "playing"},
		},
	}
}

func (g *fakeGame) Title() string { return "矿洞夺宝（测试）" }
func (g *fakeGame) State(context.Context) (kddb.State, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state, g.stateErr
}
func (g *fakeGame) Dig(_ context.Context, card, index int) (kddb.DigAnswer, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.digs = append(g.digs, [2]int{card, index})
	if g.rejectAt[index] {
		return kddb.DigAnswer{OK: false, Message: "太快了"}, nil
	}
	kind := kddb.Kind("emerald")
	if g.dangerAt[index] {
		kind = "tnt"
	}
	g.state.Game.Cards[0].Cells[index] = &kddb.OpenCell{Kind: kind}
	g.state.Me.Chances--
	return kddb.DigAnswer{OK: true, Result: &kddb.DigResult{Kind: kind, CardID: card, Index: index, Amount: 10, Text: "挖到了"}, Me: g.state.Me}, nil
}
func (g *fakeGame) Claim(context.Context) (kddb.ClaimAnswer, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.claims++
	return kddb.ClaimAnswer{OK: g.claimOK, Message: "不行", Voucher: "VOUCHER-1", Points: g.state.Me.Points}, nil
}
func (g *fakeGame) Submit(_ context.Context, v string) (string, error) {
	g.submitted = append(g.submitted, v)
	if g.submitErr != nil {
		return "", g.submitErr
	}
	return "红包站：领取 6.28", nil
}

type fakeKddb struct{ game *fakeGame }

func (f fakeKddb) Open(context.Context, auth.Session, string) (KddbGame, error) { return f.game, nil }

func withGame(t *testing.T, g *fakeGame, tweak func(*Options)) *harness {
	t.Helper()
	return start(t, true, func(o *Options) {
		o.Kddb = fakeKddb{g}
		if tweak != nil {
			tweak(o)
		}
	})
}

func (h *harness) grant(digs int) map[string]any {
	h.t.Helper()
	out, e := h.call("kddb_dig_grant", map[string]any{"packet": packetID, "digs": digs})
	if out["result"] != "granted" {
		h.t.Fatalf("grant: %v %s", out, e)
	}
	return out
}

func (h *harness) dig(grantID string, row, col int) (map[string]any, string) {
	return h.call("kddb_dig", map[string]any{"grant_id": grantID, "card": 1, "row": row, "col": col})
}

func TestTheKddbToolsAreAnExplicitList(t *testing.T) {
	tools := func(h *harness) map[string]bool {
		l, err := h.cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, tool := range l.Tools {
			m[tool.Name] = true
		}
		return m
	}
	full := tools(withGame(t, newFakeGame(), nil))
	for _, want := range []string{"kddb_state", "kddb_board", "kddb_dig_grant", "kddb_dig", "kddb_claim"} {
		if !full[want] {
			t.Errorf("missing %s", want)
		}
	}
	if len(full) != 14 {
		t.Errorf("the tools are an explicit list: %v", full)
	}
	ro := tools(withGame(t, newFakeGame(), func(o *Options) { o.ReadOnly = true }))
	for _, banned := range []string{"kddb_dig_grant", "kddb_dig", "kddb_claim", "lafish_claim"} {
		if ro[banned] {
			t.Errorf("read-only has %s", banned)
		}
	}
	if !ro["kddb_state"] || !ro["kddb_board"] {
		t.Error("read-only still reads the game")
	}
	if no := tools(start(t, true, nil)); no["kddb_state"] || no["kddb_dig"] {
		t.Error("no game wired, no kddb tools")
	}
}

func TestNoGrantNoDig(t *testing.T) {
	g := newFakeGame()
	h := withGame(t, g, nil)
	if _, e := h.dig("made-up", 1, 1); e == "" || len(g.digs) != 0 {
		t.Fatalf("a dig without a grant: err=%q digs=%v", e, g.digs)
	}
	for _, n := range []int{0, -1, 11, 1000} {
		if _, e := h.call("kddb_dig_grant", map[string]any{"packet": packetID, "digs": n}); !strings.Contains(e, "最多 10") && n > 0 {
			t.Errorf("digs=%d: %q", n, e)
		} else if e == "" {
			t.Errorf("digs=%d must be refused", n)
		}
	}
	if h.asked != 0 {
		t.Fatalf("the person is not asked about an impossible grant: asked %d", h.asked)
	}
}

func TestTheGrantIsAskedOnceAndDigsWorkInsideIt(t *testing.T) {
	g := newFakeGame()
	h := withGame(t, g, nil)
	gr := h.grant(3)
	if h.asked != 1 || gr["digs_left"].(float64) != 3 {
		t.Fatalf("asked=%d %v", h.asked, gr)
	}
	id := gr["grant_id"].(string)
	for i, cell := range [][2]int{{1, 1}, {1, 2}, {1, 3}} {
		out, e := h.dig(id, cell[0], cell[1])
		if out["result"] != "dug" || out["kind"] != "emerald" {
			t.Fatalf("dig %d: %v %s", i, out, e)
		}
		if i < 2 && out["grant_over"] != false {
			t.Fatalf("dig %d: the grant is still good: %v", i, out)
		}
		if i == 2 && (out["grant_over"] != true || out["digs_left"].(float64) != 0) {
			t.Fatalf("the last dig ends the grant: %v", out)
		}
		h.clock.advance(3 * time.Second)
	}
	if h.asked != 1 {
		t.Fatalf("the person was asked %d times: once is the whole point of a grant", h.asked)
	}
	if _, e := h.dig(id, 2, 1); e == "" || len(g.digs) != 3 {
		t.Fatalf("a fourth dig on a grant of three: err=%q digs=%d", e, len(g.digs))
	}
}

func TestDiggingTooFastSendsNothingAndCostsNothing(t *testing.T) {
	g := newFakeGame()
	h := withGame(t, g, nil)
	id := h.grant(2)["grant_id"].(string)
	h.dig(id, 1, 1)
	_, e := h.dig(id, 1, 2) // at once
	if !strings.Contains(e, "太快了") || len(g.digs) != 1 {
		t.Fatalf("err=%q digs=%d: nothing is sent when it is too soon", e, len(g.digs))
	}
	h.clock.advance(3 * time.Second)
	if out, e := h.dig(id, 1, 2); out["result"] != "dug" || out["digs_left"].(float64) != 0 {
		t.Fatalf("the dig that was too soon did not use one up: %v %s", out, e)
	}
}

func TestADangerEndsTheGrantAtOnce(t *testing.T) {
	g := newFakeGame()
	g.dangerAt[0] = true
	h := withGame(t, g, nil)
	id := h.grant(5)["grant_id"].(string)
	out, _ := h.dig(id, 1, 1)
	if out["danger"] != true || out["grant_over"] != true || !strings.Contains(out["next"].(string), "停下来") {
		t.Fatalf("%v", out)
	}
	h.clock.advance(5 * time.Second)
	if _, e := h.dig(id, 1, 2); !strings.Contains(e, "作废") || len(g.digs) != 1 {
		t.Fatalf("after a danger nothing more is dug: err=%q digs=%d", e, len(g.digs))
	}
}

func TestAFailureEndsTheGrantAndIsNotRetried(t *testing.T) {
	g := newFakeGame()
	g.rejectAt[1] = true
	h := withGame(t, g, nil)
	id := h.grant(5)["grant_id"].(string)
	if _, e := h.dig(id, 1, 2); !strings.Contains(e, "没有接受") || !strings.Contains(e, "不重试") {
		t.Fatalf("%q", e)
	}
	h.clock.advance(5 * time.Second)
	if _, e := h.dig(id, 1, 1); e == "" || len(g.digs) != 1 {
		t.Fatalf("after a failure the grant is over: err=%q digs=%d", e, len(g.digs))
	}
}

func TestACellThatCannotBeDugIsRefusedBeforeItIsSent(t *testing.T) {
	g := newFakeGame()
	h := withGame(t, g, nil)
	id := h.grant(3)["grant_id"].(string)
	for name, cell := range map[string][2]int{"fixed": {2, 2}, "off the card": {4, 1}, "row zero": {0, 1}} {
		if _, e := h.dig(id, cell[0], cell[1]); e == "" {
			t.Errorf("%s must be refused", name)
		}
	}
	if _, e := h.call("kddb_dig", map[string]any{"grant_id": id, "card": 99, "row": 1, "col": 1}); e == "" {
		t.Error("an unknown card")
	}
	if len(g.digs) != 0 {
		t.Fatalf("nothing reached the game: %v", g.digs)
	}
	h.dig(id, 1, 1)
	h.clock.advance(3 * time.Second)
	if _, e := h.dig(id, 1, 1); e == "" || len(g.digs) != 1 { // already dug
		t.Fatalf("a dug cell: err=%q digs=%d", e, len(g.digs))
	}
	// none of those refusals used up a dig of the grant of three
	if out, _ := h.dig(id, 1, 2); out["digs_left"].(float64) != 1 {
		t.Fatalf("%v", out)
	}
}

func TestTheGamesOwnCooldownIsRespected(t *testing.T) {
	g := newFakeGame()
	g.state.Me.CoolingMs = 2500
	h := withGame(t, g, nil)
	id := h.grant(2)["grant_id"].(string)
	if _, e := h.dig(id, 1, 1); !strings.Contains(e, "冷却") || len(g.digs) != 0 {
		t.Fatalf("err=%q digs=%d", e, len(g.digs))
	}
}

func TestOnlyOneGrantAtATimeAndFewPerHour(t *testing.T) {
	g := newFakeGame()
	h := withGame(t, g, nil)
	h.grant(2)
	if _, e := h.call("kddb_dig_grant", map[string]any{"packet": packetID, "digs": 2}); !strings.Contains(e, "同一时间只能有一个") {
		t.Fatalf("%q", e)
	}
	if h.asked != 1 {
		t.Fatal("the person is not asked for a second grant while one is open")
	}
	// grants are few: 3 an hour
	h2 := withGame(t, newFakeGame(), nil)
	for i := 0; i < 3; i++ {
		id := h2.grant(1)["grant_id"].(string)
		h2.dig(id, 1, 1+i%3)
		h2.clock.advance(11 * time.Second)
	}
	if _, e := h2.call("kddb_dig_grant", map[string]any{"packet": packetID, "digs": 1}); !strings.Contains(e, "上限") {
		t.Fatalf("a fourth grant in the hour: %q", e)
	}
}

func TestAGrantIsRefusedWhenThereIsNothingToDig(t *testing.T) {
	for name, tweak := range map[string]func(*kddb.State){
		"no chances": func(s *kddb.State) { s.Me.Chances = 0 },
		"over":       func(s *kddb.State) { s.Game.Over = true },
		"claimed":    func(s *kddb.State) { s.Me.Status = "claimed" },
	} {
		g := newFakeGame()
		tweak(&g.state)
		h := withGame(t, g, nil)
		if _, e := h.call("kddb_dig_grant", map[string]any{"packet": packetID, "digs": 2}); e == "" || h.asked != 0 {
			t.Errorf("%s: err=%q asked=%d", name, e, h.asked)
		}
	}
}

func TestTheGrantNeedsThePersonAndCannotBeForged(t *testing.T) {
	g := newFakeGame()
	// a client that cannot ask
	h := start(t, false, func(o *Options) { o.Kddb = fakeKddb{g} })
	if _, e := h.call("kddb_dig_grant", map[string]any{"packet": packetID, "digs": 2}); !strings.Contains(e, "确认") {
		t.Fatalf("%q", e)
	}
	// a person who says no
	d := withGame(t, g, nil)
	d.answer = "decline"
	if out, _ := d.call("kddb_dig_grant", map[string]any{"packet": packetID, "digs": 2}); out["result"] != "declined" {
		t.Fatalf("%v", out)
	}
	if _, e := d.dig("anything", 1, 1); e == "" || len(g.digs) != 0 {
		t.Fatal("no grant, no dig")
	}
	// a yes written by hand, or for another size of grant
	m := startManualWith(t, g)
	q, err := m.raw("kddb_dig_grant", map[string]any{"packet": packetID, "digs": 2}, nil, "")
	if err != nil || !q.NeedsInput() || q.RequestState == "" {
		t.Fatalf("%+v %v", q, err)
	}
	for _, c := range []struct {
		name  string
		digs  int
		state string
	}{{"no state", 2, ""}, {"forged", 2, "x|1|y"}, {"borrowed for more digs", 9, q.RequestState}} {
		res, err := m.raw("kddb_dig_grant", map[string]any{"packet": packetID, "digs": c.digs}, yes(), c.state)
		if !refused(res, err) {
			t.Errorf("%s: %+v %v", c.name, res, err)
		}
	}
	res, err := m.raw("kddb_dig_grant", map[string]any{"packet": packetID, "digs": 2}, yes(), q.RequestState)
	if refused(res, err) {
		t.Fatalf("the real pair: %+v %v", res, err)
	}
	// the same answer twice does not make a second grant
	if res, err := m.raw("kddb_dig_grant", map[string]any{"packet": packetID, "digs": 2}, yes(), q.RequestState); !refused(res, err) {
		t.Errorf("replay: %+v %v", res, err)
	}
}

func startManualWith(t *testing.T, g *fakeGame) *harness {
	t.Helper()
	return startMode(t, false, true, DefaultLimits(), func(o *Options) { o.Kddb = fakeKddb{g} })
}

func TestTheEarningsAreClaimedOnlyWhenThePersonSaysSo(t *testing.T) {
	g := newFakeGame()
	h := withGame(t, g, nil)
	h.answer = "decline"
	if out, _ := h.call("kddb_claim", map[string]any{"packet": packetID}); out["result"] != "declined" || g.claims != 0 {
		t.Fatalf("%v claims=%d", out, g.claims)
	}
	h.answer = "accept"
	h.clock.advance(11 * time.Second)
	out, e := h.call("kddb_claim", map[string]any{"packet": packetID})
	if out["result"] != "claimed" || g.claims != 1 || len(g.submitted) != 1 || g.submitted[0] != "VOUCHER-1" || !strings.Contains(out["judge_reply"].(string), "6.28") {
		t.Fatalf("%v %s claims=%d submitted=%v", out, e, g.claims, g.submitted)
	}
	// the voucher is not in what the assistant is told
	if strings.Contains(out["next"].(string)+out["judge_reply"].(string), "VOUCHER") {
		t.Error("the voucher stays with hb")
	}
}

func TestTheEarningsClaimSaysWhenSettlingFailedAndDoesNotRetry(t *testing.T) {
	g := newFakeGame()
	g.submitErr = errors.New("这个红包要求人机验证，凭证已保存到 X")
	h := withGame(t, g, nil)
	out, _ := h.call("kddb_claim", map[string]any{"packet": packetID})
	if out["result"] != "claimed_not_settled" || !strings.Contains(out["judge_reply"].(string), "人机验证") || g.claims != 1 {
		t.Fatalf("%v", out)
	}
	h.clock.advance(5 * time.Minute)
	g.state.Me.Status = "claimed"
	if _, e := h.call("kddb_claim", map[string]any{"packet": packetID}); e == "" || g.claims != 1 {
		t.Fatalf("already claimed: %q claims=%d", e, g.claims)
	}
}

func TestEverythingKddbWantsAToken(t *testing.T) {
	g := newFakeGame()
	h := withGame(t, g, func(o *Options) {
		o.Session = func(string) (auth.Session, error) {
			return auth.Session{Cookies: []auth.Cookie{{Name: "hb_session", Value: "c"}}}, nil
		}
	})
	for _, tool := range []string{"kddb_state", "kddb_board", "kddb_claim"} {
		if _, e := h.call(tool, map[string]any{"packet": packetID}); !strings.Contains(e, "--method token") {
			t.Errorf("%s with a cookie: %q", tool, e)
		}
	}
	if _, e := h.call("kddb_dig_grant", map[string]any{"packet": packetID, "digs": 2}); !strings.Contains(e, "--method token") {
		t.Errorf("grant with a cookie: %q", e)
	}
	if len(g.digs) != 0 || g.claims != 0 {
		t.Fatal("nothing sent")
	}
}

func TestTheBoardShowsTheCardAndAdviceThatIsNotAPromise(t *testing.T) {
	g := newFakeGame()
	g.state.Game.Cards[0].Cells[0] = &kddb.OpenCell{Kind: "emerald"}
	h := withGame(t, g, nil)
	out, e := h.call("kddb_board", map[string]any{"packet": packetID})
	if out == nil {
		t.Fatal(e)
	}
	grid := out["grid"].([]any)
	row0 := grid[0].([]any)
	row1 := grid[1].([]any)
	if row0[0] != "emerald" || row0[1] != "." || row1[1] != "fixed:deco" {
		t.Fatalf("%v", grid)
	}
	adv := out["advice"].(map[string]any)
	if adv["lowest_risk_cells"] == nil || !strings.Contains(out["note"].(string), "不是保证") {
		t.Fatalf("%v", out)
	}
	st, _ := h.call("kddb_state", map[string]any{"packet": packetID})
	if st["chances"].(float64) != 8 || st["dig_interval_seconds"].(float64) != 3 || st["untrusted"] == nil {
		t.Fatalf("%v", st)
	}
}
