package kddb

import (
	"fmt"
	"sort"
	"strings"
)

// The advisor's presentation: marks on cells, a line about the cell under the
// cursor, and a short panel. The maths is in advisor.go. Everything here is
// advice only: nothing in this file (or anywhere) digs for you.

type adviceCache struct {
	key string
	adv Advice
}

func (m *Model) hintsActive() bool { return m.opts.Hints && m.hintsOn }

// adviceOf is Advise for a card, remembered until something it depends on changes.
func (m *Model) adviceOf(c PublicCard) Advice {
	key := fmt.Sprintf("%d|%d|%d|%d|%d|%d", c.ID, m.st.Game.Seq, c.Opened(), len(c.Torches), len(m.st.Me.Peeks), len(c.Left))
	if m.adv.key != key {
		m.adv = adviceCache{key, Advise(c, m.st.Me.Peeks)}
	}
	return m.adv.adv
}

// notePrediction remembers the advisor's guess for a cell about to be dug (only
// when a calibration log was asked for).
func (m *Model) notePrediction(c PublicCard, idx int) {
	m.pending = nil
	if m.opts.HintLog == nil {
		return
	}
	if r, ok := predict(c, m.st.Me.Peeks, idx, m.opts.Packet); ok {
		m.pending = &r
	}
}

// recordOutcome completes that guess with what the dig turned out to be.
func (m *Model) recordOutcome(msg digMsg) {
	p := m.pending
	m.pending = nil
	if p == nil || m.opts.HintLog == nil || msg.err != nil || !msg.ans.OK || msg.ans.Result == nil {
		return
	}
	m.opts.HintLog.Record(finish(*p, msg.ans.Result))
}

// hintSafe: this undug cell cannot hold a danger, by the game's own rules.
func (m *Model) hintSafe(c PublicCard, idx int) bool {
	if !m.hintsActive() || cellAt(c, idx) != nil || c.Fixed[fmt.Sprint(idx)] != "" {
		return false
	}
	return m.adviceOf(c).IsSafe(idx)
}

// coord writes a cell as "row,column", the way the line mode's d command takes it.
func coord(size, idx int) string { return fmt.Sprintf("%d,%d", idx/size+1, idx%size+1) }

// hintText is one line about a cell, or "".
func (m *Model) hintText(c PublicCard, idx int) string {
	if !m.hintsActive() || cellAt(c, idx) != nil || c.Fixed[fmt.Sprint(idx)] != "" {
		return ""
	}
	a := m.adviceOf(c)
	switch r, known := a.Risk[idx]; {
	case a.IsSafe(idx):
		return good.Render("零风险（按规则推出，这里不可能有危险）")
	case known && r >= 1:
		return danger.Render("这里一定是危险")
	case known:
		return warn.Render(fmt.Sprintf("危险概率约 %.0f%%（估算）", r*100))
	}
	return ""
}

// hintPanel is the side panel's advisor section.
func (m *Model) hintPanel() string {
	c := m.current()
	if c == nil || !m.hintsActive() {
		return ""
	}
	a := m.adviceOf(*c)
	var b strings.Builder
	b.WriteString("\n" + bold.Render("建议") + dim.Render("（估算，H 关）") + "\n")
	if len(a.Risk) == 0 && len(a.Safe) == 0 {
		b.WriteString(dim.Render(firstNonEmpty(strings.Join(a.Notes, "；"), "没有可用的信息")) + "\n")
		return b.String()
	}
	fmt.Fprintf(&b, "%s\n", dim.Render(fmt.Sprintf("还藏着 %d 个危险在 %d 格里", a.Dangers, a.Unknown)))
	var safe []string
	for _, i := range a.Safe {
		if cellAt(*c, i) == nil {
			safe = append(safe, coord(c.Size, i))
		}
	}
	if len(safe) > 0 {
		shown := safe
		if len(shown) > 8 {
			shown = shown[:8]
		}
		more := ""
		if len(safe) > len(shown) {
			more = fmt.Sprintf(" 等 %d 格", len(safe))
		}
		b.WriteString(good.Render("零风险 ") + strings.Join(shown, " ") + dim.Render(more) + "\n")
	} else {
		b.WriteString(dim.Render("没有确定零风险的格") + "\n")
	}
	if low := a.Lowest(3); len(low) > 0 {
		var ps []string
		for _, p := range low {
			ps = append(ps, fmt.Sprintf("%s %.0f%%", coord(c.Size, p.Index), p.Risk*100))
		}
		b.WriteString(warn.Render("最低 ") + strings.Join(ps, " · ") + "\n")
	}
	if !a.Exact {
		b.WriteString(dim.Render("（有些条件没用上，仅供参考）") + "\n")
	}
	return b.String()
}

// safeCells are the cells not dug and not fixed where the rules allow no danger,
// in cell order.
func (m *Model) safeCells(c PublicCard) []int {
	var out []int
	for _, i := range m.adviceOf(c).Safe {
		if cellAt(c, i) == nil && c.Fixed[fmt.Sprint(i)] == "" {
			out = append(out, i)
		}
	}
	sort.Ints(out)
	return out
}

// jumpSafe moves the cursor to the next (dir 1) or previous (dir -1) cell with
// no danger, round the card. It moves the cursor and nothing else: digging is
// still the person's key.
func (m *Model) jumpSafe(dir int) {
	switch {
	case !m.opts.Hints:
		m.notice = "建议功能没开：启动时加 --hints（它只提示，每一下还是你来挖）"
		return
	case !m.hintsOn:
		m.notice = "建议是关的，按 H 打开"
		return
	}
	c := m.current()
	if c == nil {
		return
	}
	cells := m.safeCells(*c)
	if len(cells) == 0 {
		m.notice = "没有确定零风险的格子（建议里写着风险最低的）"
		return
	}
	target := cells[0]
	if dir < 0 {
		target = cells[len(cells)-1]
	}
	for k, i := range cells {
		if dir > 0 && i > m.cur {
			target = i
			break
		}
		if dir < 0 && i < m.cur && (k == len(cells)-1 || cells[k+1] >= m.cur) {
			target = i
			break
		}
	}
	m.cur = target
	m.notice = fmt.Sprintf("跳到 %s：按规则推出这里没有危险（共 %d 格）", coord(c.Size, target), len(cells))
}

// jumpNearestSafe is what a dig leads to when the person turned that on: the
// cursor goes to the nearest cell with no danger, if there is one, and stays
// where it is if there is not.
func (m *Model) jumpNearestSafe() {
	if !m.autoJump || !m.hintsActive() {
		return
	}
	c := m.current()
	if c == nil || c.Size == 0 {
		return
	}
	best, bestD := -1, 0
	for _, i := range m.safeCells(*c) {
		d := abs(i%c.Size-m.cur%c.Size) + abs(i/c.Size-m.cur/c.Size)
		if best < 0 || d < bestD {
			best, bestD = i, d
		}
	}
	if best < 0 || best == m.cur {
		return
	}
	m.cur = best
	m.notice += fmt.Sprintf("  光标跳到了 %s（零风险）", coord(c.Size, best))
}
