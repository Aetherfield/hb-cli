package kddb

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func colourFixture() State {
	st := shellState()
	c := card(1, 0, 0, 3)
	c.Cells[0] = &OpenCell{Kind: "emerald", UID: 7}
	c.Cells[1] = &OpenCell{Kind: "tnt", UID: 9}
	c.Cells[3] = &OpenCell{Kind: "diamond", UID: 9}
	c.Fixed = map[string]string{"4": "deco"}
	c.Torches = []Torch{{At: 5, Level: 2, V: 2}}
	st.Game.Cards = []PublicCard{c}
	st.Me.Peeks = []Peek{{CardID: 1, Index: 2, Kind: "diamond"}}
	return st
}

func run1(t *testing.T, line string) string {
	t.Helper()
	sh, out := newShell(t, recorder(colourFixture()), "")
	if _, err := sh.Exec(line); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestColourNeverChangesTheLayout(t *testing.T) {
	prev := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	for _, line := range []string{"m", "s", "ls"} {
		lipgloss.SetColorProfile(termenv.Ascii) // a pipe, or NO_COLOR
		plain := run1(t, line)
		if ansi.MatchString(plain) {
			t.Fatalf("%q: no colour codes may reach a pipe:\n%q", line, plain)
		}
		lipgloss.SetColorProfile(termenv.TrueColor)
		coloured := run1(t, line)
		if line != "ls" && !ansi.MatchString(coloured) {
			t.Fatalf("%q: expected colour on a colour terminal", line)
		}
		if got := ansi.ReplaceAllString(coloured, ""); got != plain {
			t.Fatalf("%q: colour changed the text.\nplain:    %q\ncoloured: %q", line, plain, got)
		}
		// every line is the same width with or without colour, so columns stay aligned
		pl, cl := strings.Split(plain, "\n"), strings.Split(coloured, "\n")
		for i := range pl {
			if lipgloss.Width(cl[i]) != lipgloss.Width(pl[i]) {
				t.Fatalf("%q line %d: width %d vs %d", line, i, lipgloss.Width(cl[i]), lipgloss.Width(pl[i]))
			}
		}
	}
}

func TestMapColoursFollowTheGame(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	out := run1(t, "m")
	// emerald is green (10 -> a 16-colour or truecolor code), TNT red, and the cell you dug is bold
	emeraldRow := strings.Split(out, "\n")[2]
	if !strings.Contains(emeraldRow, "\x1b[1;") && !strings.Contains(emeraldRow, "\x1b[1m") {
		t.Errorf("the cell you dug should be bold: %q", emeraldRow)
	}
	if strings.Count(out, "\x1b[") < 10 {
		t.Errorf("expected plenty of colour in the map: %q", out)
	}
}

func TestPrintLayoutForTheRecord(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
	t.Logf("\n$ s\n%s\n$ m\n%s\n$ d 3 3\n%s", run1(t, "s"), run1(t, "m"), run1(t, "d 3 3"))
}
