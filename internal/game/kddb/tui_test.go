package kddb

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestFormatUnits(t *testing.T) {
	cases := []struct {
		points int
		step   string
		want   string
	}{
		{10000, "0.01", "100.00"},
		{10000, "", "100.00"},
		{2000, "1", "2000"},
		{3, "0.5", "1.5"},
		{3, "100", "300"},
		{-250, "0.01", "-2.50"},
		{0, "0.01", "0.00"},
	}
	for _, c := range cases {
		if got := FormatUnits(c.points, c.step); got != c.want {
			t.Errorf("FormatUnits(%d, %q) = %q, want %q", c.points, c.step, got, c.want)
		}
	}
}

type useCall struct {
	item        string
	card, index int
	choice      string
}

type fakeGame struct {
	digs, claims int
	uses         []useCall
	digAnswer    DigAnswer
	useAnswer    UseAnswer
	claimAnswer  ClaimAnswer
	summary      Summary
}

func (f *fakeGame) State(context.Context) (State, error) { return State{}, nil }
func (f *fakeGame) Dig(context.Context, int, int) (DigAnswer, error) {
	f.digs++
	return f.digAnswer, nil
}
func (f *fakeGame) Use(_ context.Context, item string, card, idx int, choice string) (UseAnswer, error) {
	f.uses = append(f.uses, useCall{item, card, idx, choice})
	return f.useAnswer, nil
}
func (f *fakeGame) Summary(context.Context) (Summary, error) { return f.summary, nil }
func (f *fakeGame) Claim(context.Context) (ClaimAnswer, error) {
	f.claims++
	return f.claimAnswer, nil
}
func (f *fakeGame) Live(ctx context.Context, _ func(Live)) error {
	<-ctx.Done()
	return ctx.Err()
}
func (f *fakeGame) MyUID() int64 { return 7 }

func card(id, x, y, size int) PublicCard {
	return PublicCard{ID: id, X: x, Y: y, Size: size, Cells: make([]*OpenCell, size*size), Left: map[Kind]int{"emerald": 3}}
}

func baseState() State {
	c := card(1, 0, 0, 3)
	c.Fixed = map[string]string{"4": "deco"}
	return State{
		Game: PublicGame{Seq: 1, Left: 1000, Pool: 2000, Rules: Rules{Items: 2, DigInterval: 1}, Cards: []PublicCard{c}},
		Me:   Me{Chances: 5, Status: "playing"},
	}
}

func newModel(t *testing.T, g Game, submit SubmitFunc, opts Options, st State) *Model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := NewModel(ctx, g, submit, opts)
	m.Update(stateMsg{st: st})
	return m
}

// run executes a command and feeds its message back, like the bubbletea runtime.
func run(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	if msg := cmd(); msg != nil {
		m.Update(msg)
	}
}

func key(m *Model, s string) tea.Cmd {
	var k tea.KeyMsg
	switch s {
	case "enter":
		k = tea.KeyMsg{Type: tea.KeyEnter}
	case "right":
		k = tea.KeyMsg{Type: tea.KeyRight}
	case "esc":
		k = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	_, cmd := m.Update(k)
	return cmd
}

func TestDigRespectsCooldownAndFixedCells(t *testing.T) {
	cell := json.RawMessage(`{"type":"cell","seq":2,"cardId":1,"index":0,"cell":{"kind":"emerald","uid":7,"name":"me","at":1},"left":990,"text":"挖到绿宝石"}`)
	g := &fakeGame{digAnswer: DigAnswer{OK: true, Result: &DigResult{Text: "挖到绿宝石"}, Me: Me{Chances: 4, Points: 50, Status: "playing", CoolingMs: 60000}, Update: cell}}
	m := newModel(t, g, nil, Options{}, baseState())

	run(t, m, key(m, "enter"))
	if g.digs != 1 || m.st.Game.Cards[0].Cells[0] == nil || m.st.Me.Points != 50 || m.st.Game.Left != 990 {
		t.Fatalf("digs=%d me=%+v left=%d", g.digs, m.st.Me, m.st.Game.Left)
	}

	run(t, m, key(m, "right"))
	run(t, m, key(m, "enter")) // still cooling: no request
	if g.digs != 1 || !strings.Contains(m.notice, "还要等") {
		t.Fatalf("cooldown not respected: digs=%d notice=%q", g.digs, m.notice)
	}

	m.cool = time.Time{}
	m.cur = 4 // a fixed (deco) cell
	run(t, m, key(m, "enter"))
	if g.digs != 1 || !strings.Contains(m.notice, "不能挖") {
		t.Fatalf("fixed cell must not be dug: digs=%d notice=%q", g.digs, m.notice)
	}
}

func TestWrapUpStopsDigging(t *testing.T) {
	g := &fakeGame{}
	st := baseState()
	past := time.Now().Add(-time.Minute).UnixMilli()
	st.Game.WrapUpAt = &past
	m := newModel(t, g, nil, Options{}, st)
	run(t, m, key(m, "enter"))
	if g.digs != 0 || !strings.Contains(m.notice, "收尾") {
		t.Fatalf("digs=%d notice=%q", g.digs, m.notice)
	}
}

func TestClaimNeedsConfirmationThenSubmitsVoucher(t *testing.T) {
	g := &fakeGame{claimAnswer: ClaimAnswer{OK: true, Voucher: "kd3.v", Points: 120, Me: Me{Status: "claimed"}}}
	var submitted string
	m := newModel(t, g, func(_ context.Context, v string) (string, error) {
		submitted = v
		return "红包站已结算", nil
	}, Options{}, baseState())

	run(t, m, key(m, "c"))
	if m.mode != modeConfirm || g.claims != 0 {
		t.Fatalf("claim must ask first: mode=%v claims=%d", m.mode, g.claims)
	}
	run(t, m, key(m, "n"))
	if m.mode != modeDig || g.claims != 0 {
		t.Fatal("anything but y cancels")
	}

	run(t, m, key(m, "c"))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	_, cmd = m.Update(cmd())
	if g.claims != 1 || cmd == nil {
		t.Fatalf("claims=%d cmd=%v", g.claims, cmd)
	}
	run(t, m, cmd)
	if submitted != "kd3.v" || m.notice != "红包站已结算" || m.st.Me.Status != "claimed" {
		t.Fatalf("submitted=%q notice=%q me=%+v", submitted, m.notice, m.st.Me)
	}

	m.cool = time.Time{}
	run(t, m, key(m, "enter"))
	if g.digs != 0 {
		t.Fatal("must not dig after claiming")
	}
}

func TestSubmitFailureKeepsTheVoucherVisible(t *testing.T) {
	g := &fakeGame{claimAnswer: ClaimAnswer{OK: true, Voucher: "kd3.keepme", Points: 10, Me: Me{Status: "claimed"}}}
	m := newModel(t, g, func(context.Context, string) (string, error) { return "", context.DeadlineExceeded }, Options{}, baseState())
	run(t, m, key(m, "c"))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	_, cmd = m.Update(cmd())
	run(t, m, cmd)
	if !strings.Contains(m.notice, "kd3.keepme") {
		t.Fatalf("voucher must stay on screen when submitting fails: %q", m.notice)
	}
}

func TestAutoClaimOnlyWhenOptedIn(t *testing.T) {
	for _, auto := range []bool{false, true} {
		g := &fakeGame{claimAnswer: ClaimAnswer{OK: true, Voucher: "v", Me: Me{Status: "claimed"}}}
		m := newModel(t, g, func(context.Context, string) (string, error) { return "ok", nil }, Options{AutoClaim: auto}, baseState())
		m.claimNow = true // chances and pickaxes are used up
		cmd := m.maybeAutoClaim()
		if auto {
			run(t, m, cmd)
		}
		if got := g.claims; (got == 1) != auto {
			t.Fatalf("auto=%v: claims=%d", auto, got)
		}
		if g.digs != 0 {
			t.Fatal("auto-claim must never dig")
		}
	}
}

func TestAimedItemFlowSpyglass(t *testing.T) {
	g := &fakeGame{useAnswer: UseAnswer{OK: true, Text: "看到了", Me: Me{Spyglasses: 0, Chances: 5, Status: "playing", Peeks: []Peek{{CardID: 1, Index: 2, Kind: "diamond"}}},
		Peek: &Peek{CardID: 1, Index: 2, Kind: "diamond"}}}
	st := baseState()
	st.Me.Spyglasses = 1
	m := newModel(t, g, nil, Options{}, st)

	run(t, m, key(m, "1")) // slot 1 = spyglass
	if m.mode != modeAim || m.aim == nil || m.aim.ID != "spyglass" {
		t.Fatalf("mode=%v aim=%v", m.mode, m.aim)
	}
	if len(g.uses) != 0 {
		t.Fatal("arming must not use the item yet")
	}
	run(t, m, key(m, "right"))
	run(t, m, key(m, "enter"))
	if len(g.uses) != 1 || g.uses[0] != (useCall{"spyglass", 1, 1, ""}) {
		t.Fatalf("uses=%+v", g.uses)
	}
	if m.mode != modeDig || m.cur != 2 || !strings.Contains(m.notice, "钻石") {
		t.Fatalf("mode=%v cur=%d notice=%q", m.mode, m.cur, m.notice)
	}
	if !strings.Contains(m.View(), "你知道它是钻石") {
		t.Errorf("the peeked cell should be explained under the grid:\n%s", m.View())
	}
}

func TestConfirmItemNeedsY(t *testing.T) {
	g := &fakeGame{useAnswer: UseAnswer{OK: true, Text: "急迫！", Me: Me{Chances: 5, Status: "playing", HasteMs: 10000}}}
	st := baseState()
	st.Me.Items = &Items{Potion: 1}
	m := newModel(t, g, nil, Options{}, st)

	run(t, m, key(m, "1"))
	if m.mode != modeConfirm || len(g.uses) != 0 {
		t.Fatalf("mode=%v uses=%v", m.mode, g.uses)
	}
	run(t, m, key(m, "n"))
	if len(g.uses) != 0 {
		t.Fatal("n must cancel")
	}
	run(t, m, key(m, "1"))
	run(t, m, key(m, "y"))
	if len(g.uses) != 1 || g.uses[0].item != "potion" || g.uses[0].card != -1 {
		t.Fatalf("uses=%+v", g.uses)
	}
}

func TestEnchantingTableOffersOnlyTheGivenShapes(t *testing.T) {
	g := &fakeGame{useAnswer: UseAnswer{OK: true, Me: Me{Status: "playing"}}}
	st := baseState()
	st.Me.Items = &Items{Table: 1}
	st.Me.TableChoices = []string{"h", "x"}
	m := newModel(t, g, nil, Options{}, st)
	run(t, m, key(m, "1"))
	if m.mode != modeChoose {
		t.Fatalf("mode=%v", m.mode)
	}
	run(t, m, key(m, "v")) // not offered: cancels
	if len(g.uses) != 0 {
		t.Fatalf("a shape that was not offered must not be sent: %+v", g.uses)
	}
	run(t, m, key(m, "1"))
	run(t, m, key(m, "x"))
	if len(g.uses) != 1 || g.uses[0].choice != "x" || g.uses[0].item != "table" {
		t.Fatalf("uses=%+v", g.uses)
	}
}

func TestAimRulesDependOnTheItemRules(t *testing.T) {
	c := card(1, 0, 0, 3)
	torch, key := itemDefs[1], itemDefs[5]
	if aimProblem(torch, &c, 0, Rules{Items: 1}) == "" {
		t.Error("first item rules: a torch needs a cell below and right of the top-left")
	}
	if aimProblem(torch, &c, 4, Rules{Items: 1}) != "" {
		t.Error("first item rules: the centre cell is fine")
	}
	if aimProblem(torch, &c, 0, Rules{Items: 2}) != "" {
		t.Error("second item rules: a torch goes on any cell")
	}
	if aimProblem(key, &c, 0, Rules{Items: 2}) == "" {
		t.Error("a key only works on a vault")
	}
	c.Fixed = map[string]string{"0": "vault"}
	if aimProblem(key, &c, 0, Rules{Items: 2}) != "" {
		t.Error("a key on a vault is fine")
	}
}

func TestWalkingOffTheEdgeCrossesToTheNeighbourCard(t *testing.T) {
	st := baseState()
	st.Game.Cards = []PublicCard{card(1, 0, 0, 3), card(2, 3, 0, 3)}
	m := newModel(t, &fakeGame{}, nil, Options{}, st)
	m.cur = 5 // row 1, last column of card 1
	run(t, m, key(m, "right"))
	if c := m.current(); c.ID != 2 || m.cur != 3 {
		t.Fatalf("card=%d cur=%d, want card 2 at the left edge of the same row (3)", c.ID, m.cur)
	}
	// and nothing happens where there is no neighbour
	m.cur = 5
	run(t, m, key(m, "right"))
	if m.current().ID != 2 || m.cur != 5 {
		t.Fatalf("no card to the right: stay put (card=%d cur=%d)", m.current().ID, m.cur)
	}
}

func TestSummaryOverlay(t *testing.T) {
	g := &fakeGame{}
	g.summary.Me.Name = "me"
	g.summary.Me.Points = 500
	g.summary.Top = []TopEntry{{UID: 9, Name: "alice", Points: 900}}
	m := newModel(t, g, nil, Options{}, baseState())
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	run(t, m, cmd)
	if m.mode != modeSummary {
		t.Fatalf("mode=%v", m.mode)
	}
	if v := m.View(); !strings.Contains(v, "收益卡") || !strings.Contains(v, "alice") {
		t.Fatalf("summary view:\n%s", v)
	}
	run(t, m, key(m, "x")) // any key closes
	if m.mode != modeDig {
		t.Fatal("any key should close the overlay")
	}
}

func TestViewShowsEveryPanel(t *testing.T) {
	st := baseState()
	st.Game.Cards = []PublicCard{card(1, 0, 0, 3), card(2, 3, 0, 3)}
	st.Game.Cards[0].Torches = []Torch{{At: 0, Level: 1, Name: "x", V: 2}}
	st.Game.Cards[1].Kind = "nether"
	st.Me.Items = &Items{Torch: 2}
	st.Me.Coal = 1
	m := newModel(t, &fakeGame{}, nil, Options{}, st)
	m.width = 140
	v := m.View()
	for _, want := range []string{"矿洞夺宝", "奖池", "全图", "卡 #1", "我", "待领取", "镐子", "道具栏", "1 火把×2", "合成", "动态", "☼"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	m.width = 60 // narrow: stacked, no world map, still complete
	if nv := m.View(); strings.Contains(nv, "全图") || !strings.Contains(nv, "待领取") {
		t.Errorf("narrow layout wrong:\n%s", nv)
	}
}

func TestBannerWarnsBeforeTheWrapUp(t *testing.T) {
	st := baseState()
	soon := time.Now().Add(3 * time.Minute).UnixMilli()
	st.Game.WrapUpAt = &soon
	st.Me.Points = 100
	m := newModel(t, &fakeGame{}, nil, Options{}, st)
	if b := m.banner(); !strings.Contains(b, "收尾") {
		t.Fatalf("banner=%q", b)
	}
	st.Me.Points = 0
	m2 := newModel(t, &fakeGame{}, nil, Options{}, st)
	if m2.banner() != "" {
		t.Fatal("nothing to claim: no warning")
	}
}

func TestPushesUpdateTorchesLavaAndNoise(t *testing.T) {
	st := baseState()
	st.Game.Cards[0].Kind = "deep"
	one := 3.0
	closed := true
	l := Live{Type: "cell", Seq: 2, CardID: 1, Index: 0,
		Cell:    &OpenCell{Kind: "emerald", UID: 9, Name: "bob"},
		Torches: []Torch{{At: 1, Level: 2, V: 2}},
		Noise:   &one, Closed: &closed, Vaults: map[string]int{"5": 3},
	}
	l.Burned = []struct {
		Index int  `json:"index"`
		Kind  Kind `json:"kind"`
	}{{Index: 8, Kind: "diamond"}}
	if !st.Apply(l) {
		t.Fatal("in sync")
	}
	c := st.Game.Cards[0]
	if len(c.Torches) != 1 || c.Noise != 3 || !c.Closed || c.Vaults["5"] != 3 || c.Cells[8] == nil || c.Cells[8].Burn != 1 {
		t.Fatalf("%+v", c)
	}
}
