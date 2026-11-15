package kddb

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The world map is a picture of the mine's layout. A card's x and y are its
// place among the other cards (neighbours are (0,0) (1,0) (0,1) ...), not cell
// coordinates, so the map draws one small tile per card: its number, in its
// biome's colour, with a bar of how much of it is dug. The card you are on is
// lit. The mine can be much bigger than the window (hundreds of cards), so the
// window can be dragged.

const (
	tileW     = 4  // columns a card's tile is drawn in
	tileCols  = 5  // columns it takes on the screen: a space between tiles keeps their bars apart
	tileRows  = 2  // and rows
	worldRows = 18 // the window's height in terminal rows
)

// worldView is where the window sits on the canvas, in card places.
type worldView struct {
	panX, panY int  // places from the canvas's top-left to the window's
	centred    int  // the card the window was last centred on
	panned     bool // the person dragged it since: leave it where it is
}

// tileColor is a biome's colour for the map.
func tileColor(kind string) string {
	if c := biomeColor[kind]; c != "" {
		return c
	}
	return "245"
}

// canvasBounds is the box of places that holds every card.
func canvasBounds(cards []PublicCard) (minX, minY, maxX, maxY int) {
	minX, minY, maxX, maxY = cards[0].X, cards[0].Y, cards[0].X+1, cards[0].Y+1
	for _, c := range cards {
		minX, minY = min(minX, c.X), min(minY, c.Y)
		maxX, maxY = max(maxX, c.X+1), max(maxY, c.Y+1)
	}
	return
}

// clampPan keeps the window on the canvas; a canvas smaller than the window is centred.
func clampPan(pan, canvas, window int) int {
	if canvas <= window {
		return -((window - canvas) / 2)
	}
	return max(0, min(pan, canvas-window))
}

// cardAtCell finds the card at a place (gx, gy) of the map.
func cardAtCell(cards []PublicCard, gx, gy int) (PublicCard, bool) {
	for _, c := range cards {
		if c.X == gx && c.Y == gy {
			return c, true
		}
	}
	return PublicCard{}, false
}

// tileOf draws one card's tile: two rows of tileCols columns.
func tileOf(c PublicCard, current bool) (string, string) {
	col := tileColor(c.Kind)
	total := max(1, c.Size*c.Size)
	opened := c.Opened()
	filled := (opened*tileW + total/2) / total
	if opened > 0 && filled == 0 {
		filled = 1
	}
	top := lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("236"))
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color(col)).Background(lipgloss.Color("236"))
	switch {
	case c.Closed:
		top = top.Background(lipgloss.Color("52"))
		bar = bar.Foreground(lipgloss.Color("1")).Background(lipgloss.Color("52"))
	case opened >= total:
		top = top.Foreground(lipgloss.Color("244")) // all dug
	}
	if current {
		top = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color(col))
		bar = lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color(col))
	}
	id := fmt.Sprintf("%*d", tileW-1, c.ID%1000) + " "
	return top.Render(id) + " ", bar.Render(strings.Repeat("█", filled)+strings.Repeat("░", tileW-filled)) + " "
}

// worldMap draws the layout of the mine in a window cols terminal columns wide.
// It also works out, for the mouse, where the window is on the canvas.
func (m *Model) worldMap(cols int) string {
	cards := m.cards()
	if len(cards) == 0 {
		return dim.Render("还没有地图")
	}
	minX, minY, maxX, maxY := canvasBounds(cards)
	cw, ch := maxX-minX, maxY-minY
	vw := max(min(cols/tileCols, cw), 4)
	vh := min(max(ch, 4), worldRows/tileRows)

	cur := m.current()
	if cur != nil && m.world.centred != cur.ID {
		// a new card: bring it into view, unless the person is looking elsewhere on purpose
		m.world.centred, m.world.panned = cur.ID, false
		m.world.panX, m.world.panY = cur.X-minX-vw/2, cur.Y-minY-vh/2
	}
	m.world.panX, m.world.panY = clampPan(m.world.panX, cw, vw), clampPan(m.world.panY, ch, vh)
	ox, oy := minX+m.world.panX, minY+m.world.panY
	m.hit.minX, m.hit.minY, m.hit.vw, m.hit.vh = ox, oy, vw, vh

	var b strings.Builder
	b.WriteString(bold.Render("矿图") + dim.Render(fmt.Sprintf("  %d 张卡 · 点一张卡就去那张", len(cards))) + "\n")
	for r := 0; r < vh; r++ {
		var top, bot strings.Builder
		for k := 0; k < vw; k++ {
			c, ok := cardAtCell(cards, ox+k, oy+r)
			if !ok {
				top.WriteString(strings.Repeat(" ", tileCols))
				bot.WriteString(strings.Repeat(" ", tileCols))
				continue
			}
			t, u := tileOf(c, cur != nil && c.ID == cur.ID)
			top.WriteString(t)
			bot.WriteString(u)
		}
		b.WriteString(top.String() + "\n" + bot.String() + "\n")
	}
	more := ""
	if cw > vw || ch > vh {
		more = "拖动平移 · 滚轮上下 · "
	}
	b.WriteString(dim.Render(more + "每块下面的条＝这张卡挖了多少"))
	return b.String()
}
