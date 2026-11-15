package kddb

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// 16px icons are the heaviest case (a 7x7 card is 112 columns by 56 rows of
// half-block characters). A frame is drawn on every key press and every
// second, so it has to stay far below a human's notice.
func TestBiggestIconsRenderQuickly(t *testing.T) {
	// Measure with real colour escapes, as a true-colour terminal gets them.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	st := baseState()
	big := card(1, 0, 0, 7)
	for i := range big.Cells {
		if i%2 == 0 {
			big.Cells[i] = &OpenCell{Kind: []Kind{"emerald", "diamond", "tnt", "pearl"}[i%4], UID: 7}
		}
	}
	st.Game.Cards = []PublicCard{big}
	m := newModel(t, &fakeGame{}, nil, Options{Render: "sprite", TrueColor: true}, st)
	m.width, m.height = 220, 90
	m.zoom = 16

	if !strings.Contains(m.View(), "\x1b[38;2;") { // warm the caches; and prove the colour is really on
		t.Fatal("expected 24-bit colour escapes in the output")
	}
	const frames = 20
	start := time.Now()
	for i := 0; i < frames; i++ {
		m.cur = i % 49 // the cursor moves: a different cell is redrawn each frame
		m.View()
	}
	per := time.Since(start) / frames
	t.Logf("16px, 7x7 card: %v per frame", per)
	if per > 150*time.Millisecond {
		t.Fatalf("a frame takes %v; key presses would lag", per)
	}
}
