package kddb

import (
	"encoding/json"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func mouseModel(t *testing.T, g Game, opts Options, st State) *Model {
	t.Helper()
	opts.Render = "glyph"
	m := newModel(t, g, nil, opts, st)
	m.width = 150
	m.View() // lays out and records where things are on the screen
	return m
}

func mev(m *Model, x, y int, a tea.MouseAction, b tea.MouseButton) tea.Cmd {
	_, cmd := m.Update(tea.MouseMsg(tea.MouseEvent{X: x, Y: y, Action: a, Button: b}))
	return cmd
}

// cellXY is the screen position of a cell of the card (glyph cells are 3 columns by 1 row).
func cellXY(m *Model, idx int) (int, int) {
	n := m.current().Size
	return m.hit.gx + idx%n*3 + 1, m.hit.gy + idx/n
}

func click(t *testing.T, m *Model, x, y int) {
	t.Helper()
	run(t, m, mev(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	run(t, m, mev(m, x, y, tea.MouseActionRelease, tea.MouseButtonNone))
	m.View()
}

func digOK() *fakeGame {
	cell := json.RawMessage(`{"type":"cell","seq":2,"cardId":1,"index":4,"cell":{"kind":"emerald","uid":7,"name":"me","at":1},"left":990,"text":"ok"}`)
	return &fakeGame{digAnswer: DigAnswer{OK: true, Result: &DigResult{Text: "ok"}, Me: Me{Chances: 4, Status: "playing"}, Update: cell}}
}

func TestMouseClickAimsAndSecondClickDigs(t *testing.T) {
	g := digOK()
	m := mouseModel(t, g, Options{}, baseState())
	if !m.hit.grid {
		t.Fatal("the card's grid was not recorded")
	}
	x, y := cellXY(m, 5)
	click(t, m, x, y)
	if m.cur != 5 || g.digs != 0 {
		t.Fatalf("first click only aims: cur=%d digs=%d", m.cur, g.digs)
	}
	click(t, m, x, y)
	if g.digs != 1 {
		t.Fatalf("second click on the same cell digs: digs=%d", g.digs)
	}
}

func TestMouseDragAimsButNeverDigs(t *testing.T) {
	g := digOK()
	m := mouseModel(t, g, Options{}, baseState())
	m.cur = 0
	x0, y0 := cellXY(m, 0)
	x2, y2 := cellXY(m, 8)
	run(t, m, mev(m, x0, y0, tea.MouseActionPress, tea.MouseButtonLeft)) // armed: cursor was already here
	run(t, m, mev(m, x2, y2, tea.MouseActionMotion, tea.MouseButtonLeft))
	run(t, m, mev(m, x2, y2, tea.MouseActionRelease, tea.MouseButtonNone))
	if m.cur != 8 || g.digs != 0 {
		t.Fatalf("a drag aims and does not dig: cur=%d digs=%d", m.cur, g.digs)
	}
	// press on the cursor's cell, drag away and back: still a drag
	run(t, m, mev(m, x2, y2, tea.MouseActionPress, tea.MouseButtonLeft))
	run(t, m, mev(m, x0, y0, tea.MouseActionMotion, tea.MouseButtonLeft))
	run(t, m, mev(m, x2, y2, tea.MouseActionMotion, tea.MouseButtonLeft))
	run(t, m, mev(m, x2, y2, tea.MouseActionRelease, tea.MouseButtonNone))
	if g.digs != 0 {
		t.Fatalf("dragging off and back must not dig: digs=%d", g.digs)
	}
}

func TestMouseReleaseOffTheCellDoesNotDig(t *testing.T) {
	g := digOK()
	m := mouseModel(t, g, Options{}, baseState())
	m.cur = 5
	x, y := cellXY(m, 5)
	run(t, m, mev(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	run(t, m, mev(m, 0, 0, tea.MouseActionRelease, tea.MouseButtonNone)) // let go somewhere else
	if g.digs != 0 {
		t.Fatalf("release off the cell: digs=%d", g.digs)
	}
}

func TestMouseIgnoredWhileBusyAndInOtherModes(t *testing.T) {
	g := digOK()
	m := mouseModel(t, g, Options{}, baseState())
	x, y := cellXY(m, 5)
	m.cur = 5
	m.busy = true
	click(t, m, x, y)
	click(t, m, x, y)
	m.busy = false
	m.mode = modeHelp
	click(t, m, x, y)
	if g.digs != 0 {
		t.Fatalf("digs=%d", g.digs)
	}
}

func bigWorld() State {
	st := baseState()
	a := card(1, 0, 0, 3)
	b := card(2, 1, 0, 3) // x, y are places in the mine, one per card
	c := card(3, 40, 30, 3)
	st.Game.Cards = []PublicCard{a, b, c}
	return st
}

func TestMouseWorldClickGoesToTheCard(t *testing.T) {
	g := digOK()
	m := mouseModel(t, g, Options{}, bigWorld())
	if !m.hit.world {
		t.Fatal("the map window was not recorded")
	}
	sx := m.hit.wx + (1-m.hit.minX)*tileCols + 1 // card 2's tile
	sy := m.hit.wy + (0-m.hit.minY)*tileRows
	click(t, m, sx, sy)
	if cur := m.current(); cur == nil || cur.ID != 2 {
		t.Fatalf("clicked card 2: %v", m.current())
	}
	click(t, m, sx, sy)
	if g.digs != 0 {
		t.Fatal("a click on the map never digs")
	}
	// the tile of card 3 is far away: panning to it works
	m.world.panX, m.world.panY = 99, 99
	m.View()
	sx = m.hit.wx + (40-m.hit.minX)*tileCols
	sy = m.hit.wy + (30-m.hit.minY)*tileRows + 1
	click(t, m, sx, sy)
	if m.current().ID != 3 {
		t.Fatalf("after panning, clicked card 3: %v", m.current())
	}
}

func TestMouseWorldDragPansAndClamps(t *testing.T) {
	m := mouseModel(t, digOK(), Options{}, bigWorld())
	x, y := m.hit.wx+4, m.hit.wy+2
	px, py := m.world.panX, m.world.panY
	run(t, m, mev(m, x, y, tea.MouseActionPress, tea.MouseButtonLeft))
	run(t, m, mev(m, x-10, y-4, tea.MouseActionMotion, tea.MouseButtonLeft))
	run(t, m, mev(m, x-10, y-4, tea.MouseActionRelease, tea.MouseButtonNone))
	m.View()
	if m.world.panX != px+2 || m.world.panY != py+2 || !m.world.panned {
		t.Fatalf("pan %d,%d from %d,%d", m.world.panX, m.world.panY, px, py)
	}
	if m.current().ID != 1 {
		t.Fatal("a drag must not change the card")
	}
	// the wheel, far past the edge, clamps
	for i := 0; i < 200; i++ {
		mev(m, m.hit.wx+1, m.hit.wy+1, tea.MouseActionPress, tea.MouseButtonWheelDown)
	}
	m.View()
	_, minY, _, maxY := canvasBounds(m.cards())
	if m.world.panY > maxY-minY-m.hit.vh {
		t.Fatalf("pan not clamped: %d", m.world.panY)
	}
	for i := 0; i < 200; i++ {
		mev(m, m.hit.wx+1, m.hit.wy+1, tea.MouseActionPress, tea.MouseButtonWheelUp)
	}
	m.View()
	if m.world.panY != 0 {
		t.Fatalf("pan not clamped at the top: %d", m.world.panY)
	}
}

func TestClampPanCentresASmallCanvas(t *testing.T) {
	if got := clampPan(7, 4, 15); got != -5 {
		t.Fatalf("%d", got)
	}
	if got := clampPan(-3, 50, 15); got != 0 {
		t.Fatalf("%d", got)
	}
	if got := clampPan(99, 50, 15); got != 35 {
		t.Fatalf("%d", got)
	}
}

func TestWorldMapDrawsCardsAndNoColourStillReadable(t *testing.T) {
	st := bigWorld()
	st.Game.Cards[0].Cells[0] = &OpenCell{Kind: "emerald", UID: 7}
	m := mouseModel(t, digOK(), Options{}, st)
	v := m.View()
	if !strings.Contains(v, "矿图") || strings.Contains(v, "全图") {
		t.Fatalf("the map should be called 矿图:\n%s", v)
	}
	if !strings.Contains(v, "◆") && !strings.Contains(v, glyphs["emerald"].ch) {
		t.Fatalf("a dug cell should show its glyph:\n%s", v)
	}
}

// ---- jumping to safe cells ----

func TestJumpSafeKeysMoveOnlyTheCursor(t *testing.T) {
	g := digOK()
	m := newModel(t, g, nil, Options{Hints: true, Render: "glyph"}, hintState())
	m.width = 150
	m.cur = 0
	run(t, m, key(m, "n"))
	if m.cur != 6 {
		t.Fatalf("first safe cell after 0 is 6: %d", m.cur)
	}
	run(t, m, key(m, "n"))
	run(t, m, key(m, "n"))
	if m.cur != 8 {
		t.Fatalf("%d", m.cur)
	}
	run(t, m, key(m, "N"))
	if m.cur != 7 {
		t.Fatalf("N goes back: %d", m.cur)
	}
	m.cur = 18
	run(t, m, key(m, "n"))
	if m.cur != 6 {
		t.Fatalf("n wraps round: %d", m.cur)
	}
	m.cur = 6
	run(t, m, key(m, "N"))
	if m.cur != 18 {
		t.Fatalf("N wraps round: %d", m.cur)
	}
	if g.digs != 0 {
		t.Fatalf("jumping must never dig: %d", g.digs)
	}
}

func TestJumpSafeExplainsWhenItCannot(t *testing.T) {
	m := newModel(t, digOK(), nil, Options{Render: "glyph"}, hintState())
	run(t, m, key(m, "n"))
	if !strings.Contains(m.notice, "--hints") || m.cur != 0 {
		t.Fatalf("no --hints: %q cur=%d", m.notice, m.cur)
	}
	m = newModel(t, digOK(), nil, Options{Hints: true, Render: "glyph"}, hintState())
	run(t, m, key(m, "H"))
	run(t, m, key(m, "n"))
	if !strings.Contains(m.notice, "按 H") || m.cur != 0 {
		t.Fatalf("advisor hidden: %q", m.notice)
	}
	// nothing certain: a card with no torch
	st := hintState()
	st.Game.Cards[0].Torches = nil
	m = newModel(t, digOK(), nil, Options{Hints: true, Render: "glyph"}, st)
	run(t, m, key(m, "n"))
	if !strings.Contains(m.notice, "没有哪张卡") || m.cur != 0 {
		t.Fatalf("no safe cells: %q cur=%d", m.notice, m.cur)
	}
}

func TestAutoJumpAfterADigGoesToTheNearestSafeCellAndNeverDigs(t *testing.T) {
	cell := json.RawMessage(`{"type":"cell","seq":2,"cardId":1,"index":0,"cell":{"kind":"emerald","uid":7,"name":"me","at":1},"left":990,"text":"ok"}`)
	g := &fakeGame{digAnswer: DigAnswer{OK: true, Result: &DigResult{Text: "ok"}, Me: Me{Chances: 4, Status: "playing"}, Update: cell}}
	m := newModel(t, g, nil, Options{Hints: true, Render: "glyph"}, hintState())
	m.width = 150
	if !m.autoJump {
		t.Fatal("auto-jump follows --hints by default")
	}
	m.cur = 0
	run(t, m, key(m, "enter"))
	if g.digs != 1 {
		t.Fatalf("the one dig: %d", g.digs)
	}
	if m.cur != 6 || !strings.Contains(m.notice, "零风险") {
		t.Fatalf("nearest safe cell to 0 is 6: cur=%d notice=%q", m.cur, m.notice)
	}
	if g.digs != 1 {
		t.Fatal("jumping must not dig")
	}

	// J turns it off
	run(t, m, key(m, "J"))
	if m.autoJump {
		t.Fatal("J should turn auto-jump off")
	}
	m.cool = m.cool.Add(-1e12)
	m.cur = 12
	m.st.Game.Cards[0].Cells[12] = nil
	run(t, m, key(m, "enter"))
	if m.cur != 12 {
		t.Fatalf("auto-jump off: the cursor stays: %d", m.cur)
	}
}

func TestAutoJumpOffWithoutTheAdvisor(t *testing.T) {
	m := newModel(t, digOK(), nil, Options{Render: "glyph"}, hintState())
	if m.autoJump {
		t.Fatal("no advisor, no auto-jump")
	}
	m.jumpNearestSafe()
	if m.cur != 0 {
		t.Fatalf("%d", m.cur)
	}
}

func TestWorldMapKeepsEveryCardApart(t *testing.T) {
	// the real mine: x and y are places, neighbours differ by one
	st := baseState()
	st.Game.Cards = []PublicCard{card(1, 0, 0, 7), card(2, 1, 0, 6), card(3, 0, 1, 6), card(4, 1, 1, 6)}
	m := mouseModel(t, digOK(), Options{}, st)
	if m.hit.vw != 4 || m.hit.vh != 4 {
		t.Fatalf("2x2 places fit in the minimum window: %dx%d", m.hit.vw, m.hit.vh)
	}
	v := m.worldMap(60)
	for _, id := range []string{"1", "2", "3", "4"} {
		if !strings.Contains(v, id+" ") {
			t.Errorf("card %s missing:\n%s", id, v)
		}
	}
}

// two cards, both with a green torch in the middle: cells 6..8, 11..13, 16..18 are safe
func twoCards() State {
	st := hintState()
	b := card(2, 1, 0, 5)
	b.Left = map[Kind]int{"tnt": 4, "emerald": 21}
	b.Torches = []Torch{{At: 12, Level: 0, V: 2}}
	st.Game.Cards = append(st.Game.Cards, b)
	return st
}

func TestJumpSafeCrossesCards(t *testing.T) {
	g := digOK()
	m := newModel(t, g, nil, Options{Hints: true, Render: "glyph"}, twoCards())
	m.cur = 18 // the last safe cell of card 1
	run(t, m, key(m, "n"))
	if m.current().ID != 2 || m.cur != 6 {
		t.Fatalf("n goes on to card 2: card=%d cur=%d", m.current().ID, m.cur)
	}
	run(t, m, key(m, "N"))
	if m.current().ID != 1 || m.cur != 18 {
		t.Fatalf("N goes back to card 1's last: card=%d cur=%d", m.current().ID, m.cur)
	}
	m.selectCard(2)
	m.cur = 18
	run(t, m, key(m, "n")) // the last of the last card: round to the first
	if m.current().ID != 1 || m.cur != 6 || !strings.Contains(m.notice, "卡 #1") {
		t.Fatalf("n wraps round the mine: card=%d cur=%d %q", m.current().ID, m.cur, m.notice)
	}
	if g.digs != 0 {
		t.Fatal("jumping never digs")
	}
}

func TestJumpSafeSkipsCardsWithNoSafeCellAndClosedOnes(t *testing.T) {
	st := twoCards()
	st.Game.Cards[0].Torches = nil // card 1: nothing certain
	m := newModel(t, digOK(), nil, Options{Hints: true, Render: "glyph"}, st)
	run(t, m, key(m, "n"))
	if m.current().ID != 2 || m.cur != 6 {
		t.Fatalf("card=%d cur=%d", m.current().ID, m.cur)
	}
	m.st.Game.Cards[1].Closed = true
	m.selectCard(1)
	run(t, m, key(m, "n"))
	if !strings.Contains(m.notice, "没有哪张卡") {
		t.Fatalf("a closed card has nothing to dig: %q", m.notice)
	}
}

func TestAutoJumpGoesToTheNextCardWhenThisOneHasNone(t *testing.T) {
	st := twoCards()
	st.Game.Cards[0].Torches = nil
	m := newModel(t, digOK(), nil, Options{Hints: true, Render: "glyph"}, st)
	m.jumpNearestSafe()
	if m.current().ID != 2 || m.cur != 6 || !strings.Contains(m.notice, "卡 #2") {
		t.Fatalf("card=%d cur=%d %q", m.current().ID, m.cur, m.notice)
	}
}
