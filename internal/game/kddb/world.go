package kddb

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The world map is a picture of the mine: every cell of every card, where it
// sits, in the colour of what is in it. A cell is two columns and a row, which
// come out square; the card you are on is lit and the cell under your cursor is
// bright. The map is a window onto the canvas: when the mine is bigger than the
// window it can be dragged.

const (
	worldCols = 30 // the window, in terminal columns
	worldRows = 12 // and rows
)

// worldView is where the window sits on the canvas.
type worldView struct {
	panX, panY int  // map cells from the canvas's top-left to the window's
	centred    int  // the card the window was last centred on
	panned     bool // the person dragged it since: leave it where it is
}

// ground is the colour of a cell not dug yet, by biome: dark when it is another
// card's, lit when it is the card you are on.
var worldGround = map[string][2]string{
	"":       {"237", "242"},
	"end":    {"54", "98"},
	"trial":  {"24", "31"},
	"mine":   {"94", "137"},
	"nether": {"88", "160"},
	"deep":   {"23", "30"},
}

func worldGroundOf(biome string, lit bool) string {
	g, ok := worldGround[biome]
	if !ok {
		g = worldGround[""]
	}
	if lit {
		return g[1]
	}
	return g[0]
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

// worldCell is one cell of the picture, two columns wide.
func (m *Model) worldCell(c PublicCard, idx int, current, cursor bool) string {
	st := lipgloss.NewStyle()
	cell := cellAt(c, idx)
	text := "▒▒"
	ground := worldGroundOf(c.Kind, current)
	st = st.Background(lipgloss.Color(ground)).Foreground(lipgloss.Color("245"))
	switch {
	case cell != nil && cell.Burn == 1:
		text = "≈ "
		st = st.Background(lipgloss.Color("52")).Foreground(lipgloss.Color("202"))
	case cell != nil:
		g := glyphs[cell.Kind]
		text = orGlyph(g.ch) + " "
		col := orColor(g.color)
		fg := "0"
		if col == "8" {
			col, fg = "239", "7"
		}
		st = st.Background(lipgloss.Color(col)).Foreground(lipgloss.Color(fg))
		if IsDanger(cell.Kind) {
			st = st.Background(lipgloss.Color("1")).Foreground(lipgloss.Color("15")).Bold(true)
		}
	case c.Fixed[fmt.Sprint(idx)] != "":
		g := glyphs[Kind(c.Fixed[fmt.Sprint(idx)])]
		text = orGlyph(g.ch) + " "
		st = st.Background(lipgloss.Color("234")).Foreground(lipgloss.Color(orColor(g.color)))
	case idx == 0 && c.ID > 0:
		// the card's number, in its top-left cell while that is not dug
		text = fmt.Sprintf("%-2d", c.ID%100)
		st = st.Bold(true).Foreground(lipgloss.Color("15"))
	}
	if c.Closed {
		st = st.Background(lipgloss.Color("52"))
	}
	if cursor {
		st = st.Background(lipgloss.Color("15")).Foreground(lipgloss.Color("0")).Bold(true)
	}
	return st.Render(text)
}

// worldMap draws the window onto the mine. It also works out, for the mouse,
// where the window is on the canvas.
func (m *Model) worldMap() string {
	cards := m.cards()
	if len(cards) == 0 {
		return dim.Render("还没有地图")
	}
	minX, minY, maxX, maxY := canvasBounds(cards)
	vw, vh := worldCols/2, worldRows
	cw, ch := maxX-minX, maxY-minY

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
	b.WriteString(bold.Render("矿图") + dim.Render(fmt.Sprintf("  %d 张卡", len(cards))) + "\n")
	for r := 0; r < vh; r++ {
		for k := 0; k < vw; k++ {
			gx, gy := ox+k, oy+r
			c, ok := cardAtCell(cards, gx, gy)
			if !ok {
				b.WriteString("  ")
				continue
			}
			idx := (gy-c.Y)*c.Size + (gx - c.X)
			isCur := cur != nil && c.ID == cur.ID
			b.WriteString(m.worldCell(c, idx, isCur, isCur && idx == m.cur))
		}
		b.WriteString("\n")
	}
	more := ""
	if cw > vw || ch > vh {
		more = " · 拖拽移动"
	}
	b.WriteString(dim.Render("点卡切换"+more) + "\n")
	b.WriteString(dim.Render("▒ 没挖  ") + lipgloss.NewStyle().Background(lipgloss.Color("10")).Foreground(lipgloss.Color("0")).Render("◆ ") +
		dim.Render(" 挖到  ") + lipgloss.NewStyle().Background(lipgloss.Color("1")).Foreground(lipgloss.Color("15")).Render("✹ ") + dim.Render(" 危险"))
	return b.String()
}
