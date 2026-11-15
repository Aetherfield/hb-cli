package kddb

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	dim    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	bold   = lipgloss.NewStyle().Bold(true)
	warn   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	danger = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	good   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	title  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))
	panel  = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
)

var biomeColor = map[string]string{
	"end": "177", "trial": "74", "mine": "137", "nether": "166", "deep": "30",
}

func (m *Model) View() string {
	if m.err != nil && !m.loaded {
		return "矿洞夺宝：连不上或认不出你：" + m.err.Error() + "\n（按 q 退出，r 重试）\n"
	}
	if !m.loaded {
		return "正在认人、进入矿洞…\n"
	}
	switch m.mode {
	case modeSummary:
		return m.summaryView()
	case modeHelp:
		return m.helpView()
	}

	w := m.width
	if w < 40 {
		w = 40
	}
	var b strings.Builder
	b.WriteString(m.header(w) + "\n")

	card := m.current()
	cardView := ""
	feedLines := 7
	if card != nil {
		cardView = panel.Render(m.renderCard(*card))
		if m.spriteSize(card) > 0 {
			feedLines = 3 // pixel icons are tall: keep the chrome short
		}
	}
	me := panel.Render(m.mePanel())

	// Fit what the terminal has: world map + card + me, or card + me, or stacked.
	cardW, meW := lipgloss.Width(cardView), lipgloss.Width(me)
	const worldW = 34
	switch {
	case w >= cardW+meW+worldW:
		world := panel.Render(m.worldMap(28, 12))
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, world, cardView, me) + "\n")
	case w >= cardW+meW:
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, cardView, me) + "\n")
	default:
		b.WriteString(cardView + "\n" + me + "\n")
	}

	b.WriteString(m.feedView(feedLines) + "\n")
	if banner := m.banner(); banner != "" {
		b.WriteString(banner + "\n")
	}
	if m.notice != "" {
		b.WriteString(warn.Render(m.notice) + "\n")
	}
	b.WriteString(m.hint() + "\n")
	return b.String()
}

func (m *Model) header(w int) string {
	g := m.st.Game
	over := ""
	if g.Over {
		over = danger.Render(" 已结束")
	}
	frac := 0.0
	if g.Pool > 0 {
		frac = float64(g.Left) / float64(g.Pool)
	}
	s := fmt.Sprintf("%s%s  奖池 %s %s/%s  %d 人 · 已挖 %d 格",
		title.Render("矿洞夺宝"), over, bar(frac, 14), m.money(g.Left), m.money(g.Pool), g.Stats.Players, g.Stats.Digs)
	if g.DeadlineAt != nil {
		s += "  · 剩 " + until(*g.DeadlineAt)
	}
	return s
}

func bar(frac float64, width int) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	n := int(frac*float64(width) + 0.5)
	return good.Render(strings.Repeat("█", n)) + dim.Render(strings.Repeat("░", width-n))
}

func until(ms int64) string {
	d := time.Until(time.UnixMilli(ms))
	if d <= 0 {
		return "0"
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func cellGlyph(k Kind) (string, lipgloss.Style) {
	if g, ok := glyphs[k]; ok {
		return g.ch, lipgloss.NewStyle().Foreground(lipgloss.Color(g.color))
	}
	return "?", dim
}

// ---- the current card ----

func (m *Model) peekAt(cardID, idx int) (Kind, bool) {
	for _, p := range m.st.Me.Peeks {
		if p.CardID == cardID && p.Index == idx {
			return p.Kind, true
		}
	}
	return "", false
}

func (c PublicCard) torchAt(idx int) (Torch, bool) {
	for _, t := range c.Torches {
		if t.At == idx {
			return t, true
		}
	}
	return Torch{}, false
}

func (m *Model) renderCard(c PublicCard) string {
	var b strings.Builder
	name := "普通卡"
	if n := BiomeNames[c.Kind]; n != "" {
		name = n
	}
	head := lipgloss.NewStyle().Bold(true)
	if col := biomeColor[c.Kind]; col != "" {
		head = head.Foreground(lipgloss.Color(col))
	}
	fmt.Fprintf(&b, "%s  %s\n", head.Render(fmt.Sprintf("卡 #%d · %s", c.ID, name)), dim.Render(fmt.Sprintf("已挖 %d/%d", c.Opened(), c.Size*c.Size)))
	if c.Kind == "deep" {
		if c.Closed {
			who := ""
			if c.WokeBy != nil {
				who = fmt.Sprintf("（%s 吵醒了它，抢走 %s）", c.WokeBy.Name, m.money(c.WokeBy.Amount))
			}
			b.WriteString(danger.Render("监守者醒了，这张卡关闭 ") + who + "\n")
		} else {
			fmt.Fprintf(&b, "噪音 %s\n", warn.Render(fmt.Sprint(int(c.Noise))))
		}
	}
	gridW := c.Size * 3
	if t := m.spriteSize(&c); t > 0 {
		gridW = cardPixelSize(c.Size, t)
		b.WriteString(m.spriteGrid(c, t) + "\n")
	} else {
		for y := 0; y < c.Size; y++ {
			for x := 0; x < c.Size; x++ {
				b.WriteString(m.cellView(c, y*c.Size+x))
			}
			b.WriteString("\n")
		}
	}
	// keep the text under the grid from stretching the panel
	wrap := lipgloss.NewStyle().Width(max(gridW, 40))
	b.WriteString("\n" + wrap.Render(m.cellInfo(c)) + "\n")
	if len(c.Left) > 0 {
		var parts []string
		keys := make([]Kind, 0, len(c.Left))
		for k := range c.Left {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, k := range keys {
			if n := c.Left[k]; n > 0 {
				ch, st := cellGlyph(k)
				parts = append(parts, fmt.Sprintf("%s%s×%d", st.Render(ch), KindNames[k], n))
			}
		}
		if len(parts) > 0 {
			b.WriteString(wrap.Render(dim.Render("还剩 ")+strings.Join(parts, " ")) + "\n")
		}
	}
	return b.String()
}

// cellView draws one cell three columns wide, with a coloured background.
func (m *Model) cellView(c PublicCard, idx int) string {
	ch, fg, bg := "▒", "244", "237"
	underline := false
	cell := (*OpenCell)(nil)
	if idx < len(c.Cells) {
		cell = c.Cells[idx]
	}
	switch {
	case cell != nil && cell.Burn == 1:
		ch, fg, bg = "≈", "202", "52"
	case cell != nil:
		g, _ := glyphs[cell.Kind]
		ch, fg, bg = orGlyph(g.ch), orColor(g.color), "235"
		if cell.UID == m.game.MyUID() {
			bg = "22" // cells you dug yourself
		}
	case c.Fixed[fmt.Sprint(idx)] != "":
		g := glyphs[Kind(c.Fixed[fmt.Sprint(idx)])]
		ch, fg, bg = orGlyph(g.ch), orColor(g.color), "234"
		if c.Fixed[fmt.Sprint(idx)] == "vault" {
			if n, ok := c.Vaults[fmt.Sprint(idx)]; ok && n > 0 {
				ch = fmt.Sprint(n)
			}
		}
	default:
		if k, ok := m.peekAt(c.ID, idx); ok { // only you know this one
			g := glyphs[k]
			ch, fg, bg = orGlyph(g.ch), orColor(g.color), "53"
			underline = true
		}
	}
	if m.hintSafe(c, idx) && ch == "▒" { // the advisor: a cell that cannot hold a danger
		ch, fg = "+", "10"
	}
	if t, ok := c.torchAt(idx); ok {
		bg = torchColor(t.Level)
		if cell == nil && (ch == "▒" || ch == "+") {
			ch, fg = "☼", "16"
		}
	}
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(fg)).Background(lipgloss.Color(bg)).Underline(underline)
	sel := idx == m.cur
	if sel {
		st = st.Bold(true).Reverse(true)
		return st.Render("[" + ch + "]")
	}
	return st.Render(" " + ch + " ")
}

func orGlyph(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
func orColor(s string) string {
	if s == "" {
		return "244"
	}
	return s
}

// cellInfo says what is under the cursor, in words.
func (m *Model) cellInfo(c PublicCard) string {
	idx := m.cur
	pos := fmt.Sprintf("第 %d 行 %d 列", idx/c.Size+1, idx%c.Size+1)
	var what string
	switch cell := cellAt(c, idx); {
	case cell != nil && cell.Burn == 1:
		what = "被岩浆吞掉的" + KindNames[cell.Kind]
	case cell != nil:
		by := cell.Name + " 挖开"
		if cell.UID == m.game.MyUID() {
			by = "你挖开的"
		}
		what = fmt.Sprintf("%s（%s）", KindNames[cell.Kind], by)
	case c.Fixed[fmt.Sprint(idx)] != "":
		k := Kind(c.Fixed[fmt.Sprint(idx)])
		what = KindNames[k] + "，不能挖"
		if n, ok := c.Vaults[fmt.Sprint(idx)]; ok {
			what += fmt.Sprintf("，还能开 %d 次", n)
		}
	default:
		what = "还没挖"
		if k, ok := m.peekAt(c.ID, idx); ok {
			what += "；你知道它是" + KindNames[k]
		}
	}
	if t, ok := c.torchAt(idx); ok {
		what += "；火把：" + torchWords(t.Level, t.V)
	}
	if h := m.hintText(c, idx); h != "" {
		what += "；" + h
	}
	return dim.Render(pos+" · ") + what
}

func cellAt(c PublicCard, idx int) *OpenCell {
	if idx >= 0 && idx < len(c.Cells) {
		return c.Cells[idx]
	}
	return nil
}

// ---- the world map ----

// worldMap draws every card where it sits on the shared canvas, shaded by how
// much of it is dug, with the selected card highlighted.
func (m *Model) worldMap(maxW, maxH int) string {
	cards := m.cards()
	if len(cards) == 0 {
		return dim.Render("还没有地图")
	}
	minX, minY, maxX, maxY := cards[0].X, cards[0].Y, cards[0].X+cards[0].Size, cards[0].Y+cards[0].Size
	for _, c := range cards {
		minX, minY = min(minX, c.X), min(minY, c.Y)
		maxX, maxY = max(maxX, c.X+c.Size), max(maxY, c.Y+c.Size)
	}
	bw, bh := float64(maxX-minX), float64(maxY-minY)
	sx := float64(maxW) / bw
	if sy := float64(maxH) / bh * 2; sy < sx { // a character is about twice as tall as wide
		sx = sy
	}
	sy := sx / 2
	if sx > 2 {
		sx, sy = 2, 1
	}
	cw, ch := int(bw*sx+0.999), int(bh*sy+0.999)
	canvas := make([][]string, ch)
	for i := range canvas {
		canvas[i] = make([]string, cw)
		for j := range canvas[i] {
			canvas[i][j] = " "
		}
	}
	cur := m.current()
	shades := []string{"░", "▒", "▓", "█"}
	for _, c := range cards {
		x0, y0 := int(float64(c.X-minX)*sx), int(float64(c.Y-minY)*sy)
		w, h := max(3, int(float64(c.Size)*sx)), max(2, int(float64(c.Size)*sy))
		// leave a gap so neighbouring cards of the same biome stay apart
		if w >= 5 {
			w--
		}
		if h >= 3 {
			h--
		}
		ratio := float64(c.Opened()) / float64(c.Size*c.Size)
		shade := shades[min(3, int(ratio*4))]
		col := biomeColor[c.Kind]
		if col == "" {
			col = "250"
		}
		st := lipgloss.NewStyle().Foreground(lipgloss.Color(col))
		if c.Closed {
			st = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
		}
		if cur != nil && c.ID == cur.ID {
			st = st.Bold(true).Reverse(true)
		}
		label := fmt.Sprint(c.ID)
		for y := y0; y < y0+h && y < ch; y++ {
			for x := x0; x < x0+w && x < cw; x++ {
				s := shade
				if y == y0+h/2 && x >= x0+(w-len(label))/2 && x < x0+(w-len(label))/2+len(label) {
					s = string(label[x-(x0+(w-len(label))/2)])
				}
				canvas[y][x] = st.Render(s)
			}
		}
	}
	var b strings.Builder
	b.WriteString(bold.Render("全图") + dim.Render(fmt.Sprintf("  %d 张卡", len(cards))) + "\n")
	for _, row := range canvas {
		b.WriteString(strings.Join(row, "") + "\n")
	}
	b.WriteString(dim.Render("░▒▓█ 越深挖得越多") + "\n")
	b.WriteString(dim.Render("[ ] 或 Tab 换卡；走到卡边继续走会过去"))
	return b.String()
}

// ---- you ----

func (m *Model) mePanel() string {
	me := m.st.Me
	var b strings.Builder
	b.WriteString(bold.Render("我") + "\n")
	fmt.Fprintf(&b, "待领取  %s\n", bold.Render(m.money(me.Points)))
	if me.Chest > 0 {
		fmt.Fprintf(&b, "末影箱  %s\n", good.Render("+"+m.money(me.Chest)))
	}
	fmt.Fprintf(&b, "镐子    %s\n", dots(me.Chances, 12))
	if me.Status != "claimed" {
		wait := time.Until(m.cool)
		total := m.st.Game.Rules.DigInterval
		if total <= 0 {
			total = 1
		}
		if wait > 0 {
			fmt.Fprintf(&b, "冷却    %s %.1fs\n", bar(1-wait.Seconds()/total, 8), wait.Seconds())
		} else if !m.st.Game.Over {
			fmt.Fprintf(&b, "冷却    %s\n", good.Render("可以挖"))
		}
	}
	var status []string
	if n := me.Shields; n > 0 {
		status = append(status, fmt.Sprintf("盾牌×%d", n))
	} else if me.Shield {
		status = append(status, "盾牌")
	}
	if me.Poison > 0 {
		status = append(status, danger.Render(fmt.Sprintf("中毒×%d", me.Poison)))
	}
	if me.HasteMs > 0 {
		status = append(status, good.Render(fmt.Sprintf("急迫 %ds", me.HasteMs/1000)))
	}
	if me.SlowMs > 0 {
		status = append(status, warn.Render("蜘蛛网"))
	}
	if len(status) > 0 {
		b.WriteString("状态    " + strings.Join(status, " ") + "\n")
	}
	if me.Coal > 0 || me.Stick > 0 {
		fmt.Fprintf(&b, "合成    煤%d/2 棍%d/2 → 火把\n", me.Coal, me.Stick)
	}
	if left := time.Until(m.creeper); !m.creeper.IsZero() && left > 0 && left < 20*time.Second {
		fmt.Fprintf(&b, "%s\n", danger.Render(fmt.Sprintf("苦力怕 %ds 后炸掉 20%%！挖一格或用道具它就走", int(left.Seconds())+1)))
	}
	b.WriteString(m.hintPanel())
	b.WriteString("\n" + bold.Render("道具栏") + "\n")
	sl := slots(me)
	if len(sl) == 0 {
		b.WriteString(dim.Render("（空）") + "\n")
	}
	for i, d := range sl {
		line := fmt.Sprintf("%d %s×%d", i+1, d.Name, d.count(me))
		if m.aim != nil && m.aim.ID == d.ID {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		b.WriteString(line + "\n")
	}
	if m.mode == modeAim && m.aim != nil {
		b.WriteString(warn.Render("瞄准："+m.aim.Name) + "\n")
	} else {
		b.WriteString(dim.Render("0 拿起镐子") + "\n")
	}
	if me.Status == "claimed" {
		b.WriteString("\n" + warn.Render("已领取") + "\n")
	}
	return b.String()
}

func dots(n, max int) string {
	if n > max {
		return fmt.Sprintf("%d", n)
	}
	return strings.Repeat("●", n) + dim.Render(strings.Repeat("○", max-n)) + fmt.Sprintf(" %d", n)
}

// ---- feed, banners, hints ----

func (m *Model) feedView(lines int) string {
	feed := m.st.Game.Feed
	if len(feed) > lines {
		feed = feed[:lines]
	}
	var b strings.Builder
	b.WriteString(bold.Render("动态") + "\n")
	if len(feed) == 0 {
		b.WriteString(dim.Render("还没有人挖。") + "\n")
	}
	for _, f := range feed {
		ch, st := cellGlyph(f.Kind)
		text := f.Text
		if f.Name != "" && !strings.HasPrefix(text, f.Name) { // the game's text may already begin with the name
			text = f.Name + "  " + text
		}
		if f.UID == m.game.MyUID() {
			text = good.Render(text)
		}
		b.WriteString(st.Render(ch) + " " + text + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// banner warns about deadlines: unclaimed points are void when the packet ends.
func (m *Model) banner() string {
	g := m.st.Game
	if g.Over || m.st.Me.Status == "claimed" || m.st.Me.Points+m.st.Me.Chest <= 0 {
		return ""
	}
	if g.WrapUpAt != nil && m.wrapUpStarted() {
		return danger.Render("已进入收尾：不能再挖了。截止后没领的奖励作废！按 c 领取。")
	}
	if g.WrapUpAt != nil {
		if d := time.Until(time.UnixMilli(*g.WrapUpAt)); d < 5*time.Minute {
			return warn.Render(fmt.Sprintf("再过 %s 进入收尾，到时不能再挖，别忘了领取（c）。", until(*g.WrapUpAt)))
		}
	}
	if m.claimNow {
		return warn.Render("镐子和附魔镐都用完了：按 c 领取。")
	}
	return ""
}

func (m *Model) hint() string {
	switch m.mode {
	case modeAim:
		return dim.Render("←↑↓→ 移动  回车 使用  Esc 取消  0 拿起镐子")
	case modeConfirm, modeChoose:
		return ""
	}
	return dim.Render("←↑↓→/hjkl 移动  空格/回车 挖  1-9 道具  [ ] 换卡  c 领取  g 收益卡  z 缩放  r 刷新  ? 帮助  q 退出")
}

// ---- overlays ----

func (m *Model) summaryView() string {
	s := m.summary
	if s == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(title.Render("收益卡") + "  " + dim.Render(s.Me.Name) + "\n\n")
	fmt.Fprintf(&b, "待领取 %s · 挖了 %d 格 · 最大一笔 %s\n", bold.Render(m.money(s.Me.Points)), s.Me.Digs, m.money(s.Me.Best))
	fmt.Fprintf(&b, "踩 TNT %d 次 · 盾牌挡住 %d 次 · 一共被扣 %s\n\n", s.Me.TNT, s.Me.Blocked, m.money(s.Me.Lost))
	if len(s.Me.Trail) > 0 {
		b.WriteString(bold.Render("逐格记录") + dim.Render("（最近 12 条）") + "\n")
		tr := s.Me.Trail
		if len(tr) > 12 {
			tr = tr[len(tr)-12:]
		}
		for _, st := range tr {
			ch, sty := cellGlyph(Kind(st.K))
			blocked := ""
			if st.B == 1 {
				blocked = dim.Render(" 盾牌挡住")
			}
			fmt.Fprintf(&b, "%s %-8s %+8s  手上 %s%s\n", sty.Render(ch), kindLabel(st.K), m.money(st.A), m.money(st.P), blocked)
		}
		b.WriteString("\n")
	}
	b.WriteString(bold.Render("全场前几名") + "\n")
	for i, t := range s.Top {
		if i >= 10 {
			break
		}
		line := fmt.Sprintf("%2d. %-16s %s", i+1, t.Name, m.money(t.Points))
		if t.UID == m.game.MyUID() {
			line = good.Render(line)
		}
		b.WriteString(line + "\n")
	}
	fmt.Fprintf(&b, "\n%s\n", dim.Render(fmt.Sprintf("%d 人参与 · 已领 %d 人 · 奖池还剩 %s · 按任意键返回", s.Stats.Players, s.Stats.Claimed, m.money(s.Left))))
	return b.String()
}

func kindLabel(k string) string {
	switch k {
	case "poison":
		return "中毒"
	case "creeper":
		return "苦力怕"
	case "warden":
		return "监守者"
	case "vault":
		return "宝库"
	case "ender":
		return "末影箱"
	}
	if n := KindNames[Kind(k)]; n != "" {
		return n
	}
	return k
}

func (m *Model) helpView() string {
	var b strings.Builder
	b.WriteString(title.Render("矿洞夺宝 · 终端版") + "\n\n")
	b.WriteString(bold.Render("目标") + "  在地图上挖格子攒“待领取”，见好就收；踩到 TNT 会扣一部分，什么时候领取自己定。\n")
	b.WriteString("       截止后没领的奖励作废，钱退回发红包的人。\n\n")
	b.WriteString(bold.Render("按键") + "\n")
	b.WriteString("  ←↑↓→ / hjkl   移动光标（走到卡边会过到相邻的卡）\n")
	b.WriteString("  空格 / 回车   挖这一格（有挖掘间隔，服务器把关）\n")
	b.WriteString("  [ ]  Tab      换卡\n")
	b.WriteString("  1-9           取道具栏里第 n 个道具：瞄准类先移动再回车使用，药水/藏宝图/末影箱要确认，附魔台选形状\n")
	b.WriteString("  0 / Esc       放下道具，拿起镐子\n")
	b.WriteString("  c             领取（要确认，领取后不能再挖）\n")
	b.WriteString("  g             收益卡：逐格记录和全场前几名\n")
	b.WriteString("  z             缩放：自动 → 符号 → 4/6/8/16 像素图标（16 是原版贴图的原始大小，一格占 16 列 8 行）。把终端字体调小，格子就更大更清楚\n")
	b.WriteString("  r             重新拉取状态     q  退出\n")
	b.WriteString("  H             显示/隐藏建议（启动时要加 --hints 才有）：绿色 + 是按规则推出不可能有危险的格子，\n")
	b.WriteString("                光标所在格会写危险概率（估算）。它只给建议，挖不挖、挖哪格都是你按键\n\n")
	b.WriteString(bold.Render("图标") + "  像素图标要真彩色终端；想用 Minecraft 原版贴图：运行 hb assets fetch（现场从 Mojang 官方取，不随 hb 分发）\n\n")
	b.WriteString(bold.Render("格子") + "\n  ")
	for _, k := range []Kind{"emerald", "diamond", "apple", "totem", "tnt", "pearl", "eye", "shield", "spyglass", "crystal", "coal", "stick", "map", "potion", "key", "vault", "lava", "cobweb", "spawner", "minecart", "ender"} {
		ch, st := cellGlyph(k)
		b.WriteString(st.Render(ch) + KindNames[k] + "  ")
	}
	b.WriteString("\n\n" + bold.Render("颜色") + "  绿底=你自己挖开的  紫底带下划线=只有你知道（望远镜/藏宝图）\n")
	b.WriteString("       火把：绿=周围没危险  黄=1–2  橙=3–4  红=更多\n\n")
	b.WriteString(dim.Render("每一次挖掘和使用道具都是你按一次键；hb 不会自动连续操作。按任意键返回。") + "\n")
	return b.String()
}
