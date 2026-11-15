package kddb

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// The mouse. It does what the keys do, no more: a click or a drag moves the
// cursor, and a dig still needs a deliberate second act on the same cell (a
// second click, or Enter), so a stray click never digs.
//
//   - on the card: press a cell to put the cursor there; drag to aim; press the
//     cell the cursor is already on, and let go on it, to dig.
//   - on the map: click a card to go to it (and to the cell you clicked);
//     drag to pan the window; the wheel pans up and down.

// hitMap says where the things that can be clicked are on the screen, as of the
// last View.
type hitMap struct {
	// the card's grid
	grid   bool
	gx, gy int // top-left cell of the grid on the screen
	size   int // cells per side
	sprite int // 0: glyph cells (3 columns, 1 row); else the icon size in pixels
	cardID int

	// the map window
	world          bool
	wx, wy, ww, wh int // the window on the screen, in terminal cells
	minX, minY     int // the map cell at its top-left
	vw, vh         int // its size in map cells
}

// cellAt is the cell of the card under a screen position.
func (h hitMap) cellAt(sx, sy int) (int, bool) {
	if !h.grid || h.size == 0 {
		return 0, false
	}
	x, y := sx-h.gx, sy-h.gy
	if x < 0 || y < 0 {
		return 0, false
	}
	var cx, cy int
	if h.sprite == 0 {
		cx, cy = x/3, y
	} else {
		// pixel tiles: tileSize wide with a gap, inside the card's frame; a row is two pixels
		step := tileSize(h.sprite) + tileGap
		px, py := x-cardFrame, y*2+1-cardFrame
		if px < 0 || py < 0 || px%step >= tileSize(h.sprite) || py%step >= tileSize(h.sprite) {
			return 0, false
		}
		cx, cy = px/step, py/step
	}
	if cx >= h.size || cy >= h.size {
		return 0, false
	}
	return cy*h.size + cx, true
}

// inWorld says whether a screen position is in the map window.
func (h hitMap) inWorld(sx, sy int) bool {
	return h.world && sx >= h.wx && sx < h.wx+h.ww && sy >= h.wy && sy < h.wy+h.wh
}

// worldCellAt is the map cell under a screen position: two columns to a cell.
func (h hitMap) worldCellAt(sx, sy int) (gx, gy int, ok bool) {
	if !h.inWorld(sx, sy) {
		return 0, 0, false
	}
	return h.minX + (sx-h.wx)/tileCols, h.minY + (sy-h.wy)/tileRows, true
}

// dragState is a button held down.
type dragState struct {
	on    bool
	world bool // started on the map; else on the card
	sx    int  // where it started, on the screen
	sy    int
	panX  int // the window's pan when it started
	panY  int
	moved bool
	// pressing the cell the cursor was already on, and letting go on it, digs
	armed bool
	cell  int
}

func (m *Model) mouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.loaded || (m.mode != modeDig && m.mode != modeAim) {
		return m, nil
	}
	ev := tea.MouseEvent(msg)
	c := m.current()
	switch {
	case ev.Button == tea.MouseButtonWheelUp || ev.Button == tea.MouseButtonWheelDown:
		if m.hit.inWorld(ev.X, ev.Y) {
			d := -1
			if ev.Button == tea.MouseButtonWheelDown {
				d = 1
			}
			m.world.panY += d
			m.world.panned = true
		}
	case ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft:
		m.drag = dragState{on: true, sx: ev.X, sy: ev.Y, panX: m.world.panX, panY: m.world.panY}
		if idx, ok := m.hit.cellAt(ev.X, ev.Y); ok && c != nil {
			m.drag.cell = idx
			m.drag.armed = idx == m.cur
			m.cur = idx
			if !m.drag.armed {
				m.notice = "再点一次这格才挖（也可以按 Enter）；拖动是瞄准，松手不会挖。"
			}
		} else if m.hit.inWorld(ev.X, ev.Y) {
			m.drag.world = true
		} else {
			m.drag.on = false
		}
	case ev.Action == tea.MouseActionMotion && m.drag.on:
		if m.drag.world {
			dx, dy := (ev.X-m.drag.sx)/tileCols, (ev.Y-m.drag.sy)/tileRows
			if dx != 0 || dy != 0 {
				m.drag.moved = true
				m.world.panned = true
				m.world.panX, m.world.panY = m.drag.panX-dx, m.drag.panY-dy
			}
			break
		}
		if idx, ok := m.hit.cellAt(ev.X, ev.Y); ok && c != nil {
			if idx != m.drag.cell {
				m.drag.moved = true // dragging off the cell it started on: aiming, not digging
				m.drag.armed = false
			}
			m.drag.cell, m.cur = idx, idx
		}
	case ev.Action == tea.MouseActionRelease && m.drag.on:
		d := m.drag
		m.drag = dragState{}
		switch {
		case d.world && !d.moved:
			return m.clickWorld(ev.X, ev.Y)
		case !d.world && d.armed && !d.moved && c != nil:
			if idx, ok := m.hit.cellAt(ev.X, ev.Y); ok && idx == m.cur {
				return m.act(c)
			}
		}
	}
	return m, nil
}

// clickWorld goes to the card whose tile was clicked. Digging is done on the
// card itself, so a click on the map never digs.
func (m *Model) clickWorld(sx, sy int) (tea.Model, tea.Cmd) {
	gx, gy, ok := m.hit.worldCellAt(sx, sy)
	if !ok {
		return m, nil
	}
	card, ok := cardAtCell(m.cards(), gx, gy)
	if !ok {
		return m, nil
	}
	m.selectCard(card.ID)
	m.world.centred, m.world.panned = card.ID, true // it was clicked where it is: leave the window be
	m.notice = fmt.Sprintf("到了卡 #%d；在右边那张卡上点格子瞄准，再点一次才挖。", card.ID)
	return m, nil
}
