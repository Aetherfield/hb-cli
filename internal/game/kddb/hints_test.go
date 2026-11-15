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
	c.Left = map[Kind]int{"tnt": 4, "emerald": 21}
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
