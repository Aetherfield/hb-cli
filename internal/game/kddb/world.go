package kddb

import (
	"fmt"
	"strings"
)

// The world map is a picture of the mine: every cell of every card, where it
// sits, in the colour of what is in it. A cell is two columns and a row, which
// come out square; the card you are on is lit and the cell under your cursor is
// bright. The map is a window onto the canvas: when the mine is bigger than the
// window it can be dragged.

const (
	worldRows = 16 // the window's height, in map cells (a row is a row)
)

// worldView is where the window sits on the canvas.
type worldView struct {
	panX, panY int  // map cells from the canvas's top-left to the window's
	centred    int  // the card the window was last centred on
	panned     bool // the person dragged it since: leave it where it is
}

// canvasBounds is the box that holds every card, in map cells.
func canvasBounds(cards []PublicCard) (minX, minY, maxX, maxY int) {
	minX, minY, maxX, maxY = cards[0].X, cards[0].Y, cards[0].X+cards[0].Size, cards[0].Y+cards[0].Size
	for _, c := range cards {
		minX, minY = min(minX, c.X), min(minY, c.Y)
		maxX, maxY = max(maxX, c.X+c.Size), max(maxY, c.Y+c.Size)
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

// cardAtCell finds the card that holds a map cell.
func cardAtCell(cards []PublicCard, gx, gy int) (PublicCard, bool) {
	for _, c := range cards {
		if gx >= c.X && gx < c.X+c.Size && gy >= c.Y && gy < c.Y+c.Size {
			return c, true
		}
	}
	return PublicCard{}, false
}

// worldMap draws the mine the way the game shows it: every card where it sits,
// its cells drawn exactly as on the card panel (same glyphs and colours), so
// the map is the board. cols is how many map cells fit across. It also works
// out, for the mouse, where the window is on the canvas.
func (m *Model) worldMap(cols int) string {
	cards := m.cards()
	if len(cards) == 0 {
		return dim.Render("还没有地图")
	}
	minX, minY, maxX, maxY := canvasBounds(cards)
	cw, ch := maxX-minX, maxY-minY
	vw, vh := max(min(cols, cw), 6), min(max(ch, 6), worldRows)

	cur := m.current()
	if cur != nil && m.world.centred != cur.ID {
		// a new card: bring it into view, unless the person is looking elsewhere on purpose
		m.world.centred, m.world.panned = cur.ID, false
		m.world.panX, m.world.panY = cur.X-minX+cur.Size/2-vw/2, cur.Y-minY+cur.Size/2-vh/2
	}
	m.world.panX, m.world.panY = clampPan(m.world.panX, cw, vw), clampPan(m.world.panY, ch, vh)
	ox, oy := minX+m.world.panX, minY+m.world.panY
	m.hit.minX, m.hit.minY, m.hit.vw, m.hit.vh = ox, oy, vw, vh

	var b strings.Builder
	b.WriteString(bold.Render("矿图") + dim.Render(fmt.Sprintf("  %d 张卡 · 点格子选中，再点一次挖", len(cards))) + "\n")
	for r := 0; r < vh; r++ {
		for k := 0; k < vw; k++ {
			gx, gy := ox+k, oy+r
			c, ok := cardAtCell(cards, gx, gy)
			if !ok {
				b.WriteString("   ")
				continue
			}
			idx := (gy-c.Y)*c.Size + (gx - c.X)
			b.WriteString(m.cellDraw(c, idx, cur != nil && c.ID == cur.ID && idx == m.cur))
		}
		b.WriteString("\n")
	}
	more := ""
	if cw > vw || ch > vh {
		more = "拖动平移 · 滚轮上下"
	}
	b.WriteString(dim.Render(more))
	return b.String()
}
