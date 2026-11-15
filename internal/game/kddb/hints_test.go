package kddb

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// hintState is a 5x5 card with 4 dangers left and a green torch in the middle:
// the 3x3 around cell 12 (row 3, column 3) cannot hold a danger.
func hintState() State {
	st := baseState()
	c := card(1, 0, 0, 5)
	c.Left = map[Kind]int{"tnt": 4, "emerald": 11, "coal": 10} // two harmless kinds: the numbers cannot name a safe cell
	c.Torches = []Torch{{At: 12, Level: 0, V: 2}}
	st.Game.Cards = []PublicCard{c}
	return st
}

func TestTheAdvisorIsOffUnlessAskedFor(t *testing.T) {
	m := newModel(t, &fakeGame{}, nil, Options{Render: "glyph"}, hintState())
	m.width = 140
	c := m.st.Game.Cards[0]
	if m.hintSafe(c, 6) || m.hintText(c, 6) != "" || m.hintPanel() != "" {
		t.Fatal("without --hints the advisor must say nothing")
	}
	v := m.View()
	for _, banned := range []string{"建议", "零风险", "危险概率"} {
		if strings.Contains(v, banned) {
			t.Errorf("the screen must not mention %q without --hints:\n%s", banned, v)
		}
	}
	run(t, m, key(m, "H"))
	if !strings.Contains(m.notice, "--hints") || m.hintsActive() {
		t.Fatalf("H must explain that --hints is needed, not switch it on: %q", m.notice)
	}
	// and the line mode refuses too
	g := recorder(hintState())
	sh, out := newShell(t, g, "")
	if _, err := sh.Exec("hint"); err == nil || !strings.Contains(err.Error(), "--hints") {
		t.Fatalf("hint without --hints: %v", err)
	}
	if _, err := sh.Exec("m"); err != nil || strings.Contains(out.String(), "+") {
		t.Fatalf("the map must carry no advisor marks: %q (%v)", out.String(), err)
	}
}

func TestTheAdvisorMarksCellsAndExplainsThem(t *testing.T) {
	m := newModel(t, &fakeGame{}, nil, Options{Hints: true, Render: "glyph"}, hintState())
	m.width = 150
	c := m.st.Game.Cards[0]

	for _, idx := range []int{6, 7, 8, 11, 12, 13, 16, 17, 18} { // inside the green torch's 3x3
		if !m.hintSafe(c, idx) {
			t.Errorf("cell %d should be marked safe", idx)
		}
	}
	if m.hintSafe(c, 0) {
		t.Error("a corner cell outside the torch is not certain")
	}
	m.cur = 6
	if got := m.hintText(c, 6); !strings.Contains(got, "零风险") {
		t.Errorf("the cursor line for a safe cell: %q", got)
	}
	m.cur = 0
	if got := m.hintText(c, 0); !strings.Contains(got, "25%") || !strings.Contains(got, "估算") {
		t.Errorf("4 dangers over the 16 cells outside the torch is 25%%, and it must say it is an estimate: %q", got)
	}
	v := m.View()
	for _, want := range []string{"建议", "零风险", "2,2", "最低", "25%"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
	if !strings.Contains(v, "+") {
		t.Error("safe cells are drawn with a +")
	}

	// H hides and shows it
	run(t, m, key(m, "H"))
	if v := m.View(); m.hintsActive() || strings.Contains(v, "零风险") || strings.Contains(v, "危险概率") {
		t.Fatal("H should hide the advisor")
	}
	run(t, m, key(m, "H"))
	if !m.hintsActive() {
		t.Fatal("H should show it again")
	}
}

func TestTheAdvisorUpdatesWhenTheBoardChanges(t *testing.T) {
	m := newModel(t, &fakeGame{}, nil, Options{Hints: true}, hintState())
	c := m.st.Game.Cards[0]
	before := m.adviceOf(c)
	if before.Unknown != 25 {
		t.Fatalf("%+v", before)
	}
	// someone digs a danger: the card now says 3 left, and the advice must follow
	m.st.Game.Cards[0].Cells[0] = &OpenCell{Kind: "tnt", UID: 9}
	m.st.Game.Cards[0].Left["tnt"] = 3
	m.st.Game.Seq++
	after := m.adviceOf(m.st.Game.Cards[0])
	if after.Dangers != 3 || after.Unknown != 24 {
		t.Fatalf("stale advice after the board changed: %+v", after)
	}
}

func TestLineModeHint(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	g := recorder(hintState())
	sh, out := newShell(t, g, "")
	sh.Hints = true
	if _, err := sh.Exec("hint"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"#1 还藏着 4 个危险在 25 格里（估算）", "零风险 2,2 2,3 2,4 3,2 3,3 3,4 4,2 4,3 4,4", "最低 1,1 25%"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// the map marks the same cells
	out.Reset()
	if _, err := sh.Exec("m"); err != nil {
		t.Fatal(err)
	}
	// nine cells are safe; the middle one holds the torch, whose colour digit takes the marker's place
	want := "#1 普通 5x5 0/25\n  1   2   3   4   5\n1 .   .   .   .   .\n2 .   . + . + . + .\n3 .   . + . 0 . + .\n4 .   . + . + . + .\n5 .   .   .   .   .\n" +
		"0 火把 绿  + 零风险（--hints）\n"
	if out.String() != want {
		t.Fatalf("map:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestAdvisingNeverDigs(t *testing.T) {
	g := recorder(hintState())
	sh, _ := newShell(t, g, "")
	sh.Hints = true
	for _, line := range []string{"hint", "x", "m", "s", "hint"} {
		if _, err := sh.Exec(line); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
	}
	if len(g.digged) != 0 || len(g.uses) != 0 || g.claimed != 0 {
		t.Fatalf("advice must never act: digs %v uses %v claims %d", g.digged, g.uses, g.claimed)
	}
	// and there is no command that digs "the safest" cell for you
	for _, line := range []string{"safe", "best", "dig-safe", "autodig", "d safe"} {
		if _, err := sh.Exec(line); err == nil {
			t.Errorf("%q must not exist or work", line)
		}
	}
	if len(g.digged) != 0 {
		t.Fatalf("nothing may have dug: %v", g.digged)
	}
}

func TestTUINeverDigsOnItsOwnWithHintsOn(t *testing.T) {
	g := &fakeGame{}
	m := newModel(t, g, nil, Options{Hints: true}, hintState())
	// every kind of update the screen reacts to, none of which may dig
	m.Update(tickMsg{})
	m.Update(liveMsg(Live{Type: "view"}))
	m.View()
	m.hintPanel()
	if g.digs != 0 {
		t.Fatalf("the advisor and the screen must never dig: %d", g.digs)
	}
}

// one cell left undug and one danger left: that cell is certainly the danger
func lastDangerState() State {
	st := baseState()
	c := card(1, 0, 0, 3)
	c.Left = map[Kind]int{"tnt": 1, "crystal": 1} // two kinds of danger: it cannot say which is which
	for i := range c.Cells {
		if i != 5 && i != 8 {
			c.Cells[i] = &OpenCell{Kind: "emerald", UID: 9}
		}
	}
	st.Game.Cards = []PublicCard{c}
	return st
}

func TestTheAdvisorMarksCellsThatAreCertainlyDangers(t *testing.T) {
	m := newModel(t, &fakeGame{}, nil, Options{Hints: true, Render: "glyph"}, lastDangerState())
	m.width = 150
	c := m.st.Game.Cards[0]
	if !m.hintDanger(c, 5) || m.hintSafe(c, 5) || m.hintDanger(c, 0) {
		t.Fatal("cell 5 is the danger, and only it")
	}
	v := m.View()
	if !strings.Contains(v, "✗") || !strings.Contains(v, "一定危险") || !strings.Contains(v, "2,3") {
		t.Fatalf("the screen must mark it and list it:\n%s", v)
	}
	// the jump never goes to it
	run(t, m, key(m, "n"))
	if m.cur == 5 {
		t.Fatal("n must not jump to a certain danger")
	}
	// hidden with H, and absent without --hints
	run(t, m, key(m, "H"))
	if v := m.View(); strings.Contains(v, "✗") || strings.Contains(v, "一定危险") {
		t.Fatal("H hides it")
	}
	off := newModel(t, &fakeGame{}, nil, Options{Render: "glyph"}, lastDangerState())
	if off.hintDanger(off.st.Game.Cards[0], 5) || strings.Contains(off.View(), "✗") {
		t.Fatal("no marks without --hints")
	}
}

func TestLineModeMapMarksCertainDangers(t *testing.T) {
	sh, out := newShell(t, recorder(lastDangerState()), "")
	sh.Hints = true
	if _, err := sh.Exec("hint"); err != nil || !strings.Contains(out.String(), "一定危险") || !strings.Contains(out.String(), "2,3") {
		t.Fatalf("hint: %q (%v)", out.String(), err)
	}
}

func TestWhenTheNumbersSettleACellItIsDrawnAsWhatItIs(t *testing.T) {
	// one cell undug, and the card says only a TNT is left: that cell is TNT
	st := baseState()
	c := card(1, 0, 0, 3)
	c.Left = map[Kind]int{"tnt": 1}
	for i := range c.Cells {
		if i != 5 {
			c.Cells[i] = &OpenCell{Kind: "emerald", UID: 9}
		}
	}
	st.Game.Cards = []PublicCard{c}
	m := newModel(t, &fakeGame{}, nil, Options{Hints: true, Render: "glyph"}, st)
	m.width = 150
	if k, ok := m.hintKnown(c, 5); !ok || k != "tnt" {
		t.Fatalf("known: %v %v", k, ok)
	}
	m.cur = 5
	v := m.View()
	if !strings.Contains(v, glyphs["tnt"].ch) || !strings.Contains(v, "按规则推出它是") {
		t.Fatalf("it should be drawn as TNT and said so:\n%s", v)
	}
	// without --hints nothing is deduced on screen
	off := newModel(t, &fakeGame{}, nil, Options{Render: "glyph"}, st)
	if _, ok := off.hintKnown(c, 5); ok {
		t.Fatal("no deduced kinds without --hints")
	}
}

func TestDeducedKindsNeedCountsThatAddUp(t *testing.T) {
	c := card(1, 0, 0, 3)
	for i := range c.Cells {
		if i != 5 && i != 8 {
			c.Cells[i] = &OpenCell{Kind: "emerald", UID: 9}
		}
	}
	// two undug cells but the counts say three things are left: say nothing
	c.Left = map[Kind]int{"tnt": 1, "emerald": 2}
	if a := Advise(c, nil); len(a.Known) != 0 {
		t.Fatalf("counts that do not add up must not name anything: %v", a.Known)
	}
	// two cells, two kinds left, neither certain: nothing either
	c.Left = map[Kind]int{"tnt": 1, "emerald": 1}
	if a := Advise(c, nil); len(a.Known) != 0 {
		t.Fatalf("a coin flip is not a deduction: %v", a.Known)
	}
	// every cell the same kind left: all of them are known
	c.Left = map[Kind]int{"emerald": 2}
	if a := Advise(c, nil); a.Known[5] != "emerald" || a.Known[8] != "emerald" {
		t.Fatalf("%v", a.Known)
	}
	// what you saw yourself comes off the counts: cell 5 seen as TNT leaves cell 8 as the emerald
	c.Left = map[Kind]int{"tnt": 1, "emerald": 1}
	a := Advise(c, []Peek{{CardID: 1, Index: 5, Kind: "tnt"}})
	if a.Known[8] != "emerald" {
		t.Fatalf("%v", a.Known)
	}
	if _, ok := a.Known[5]; ok {
		t.Fatal("what you saw is drawn as a peek, not as a deduction")
	}
}
