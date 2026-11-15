package kddb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Shell is the quiet way to play: no full screen, one line in, one line (or a
// small text map) out. Every command does exactly one thing; there is no
// repeat or loop, and the game's own dig interval and per-person chances still
// decide what is allowed. It can run a single command (-x), or read commands
// from stdin.
type Shell struct {
	seenKinds map[Kind]bool   // what the last map drew, for its legend
	seenMarks map[string]bool //
	ctx       context.Context
	game      Game
	submit    SubmitFunc
	scan      *bufio.Scanner
	out       io.Writer

	st     State
	loaded bool
	at     time.Time // when the state was last fetched
	card   int       // the current card's id
	step   string    // smallest money unit, for printing
	cool   time.Time

	// Hints turns on the advisor (advisor.go): the hint command, and a green +
	// on cells that cannot hold a danger. Off unless asked for; it only advises.
	Hints   bool
	mapHint *Advice // set while the map is drawn

	// HintLog, if set, records the advisor's guess for each cell you dig and what
	// it turned out to be (hb hints report). Packet is written into it.
	HintLog HintLog
	Packet  string
}

// NewShell reads commands from in and writes results to out.
func NewShell(ctx context.Context, g Game, submit SubmitFunc, in io.Reader, out io.Writer) *Shell {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	return &Shell{ctx: ctx, game: g, submit: submit, scan: sc, out: out, step: "0.01"}
}

func (s *Shell) say(format string, a ...any) { fmt.Fprintf(s.out, format+"\n", a...) }

// Run reads commands until "q" or the end of input. prompt says whether to
// print a "> " prompt (only when a person is typing).
func (s *Shell) Run(prompt bool) error {
	if prompt {
		s.say("矿洞夺宝 · 行模式（h 看命令，b 老板键，q 退出）")
	}
	for {
		if prompt {
			fmt.Fprint(s.out, "> ")
		}
		if !s.scan.Scan() {
			return s.scan.Err()
		}
		quit, err := s.Exec(s.scan.Text())
		if err != nil {
			s.say("! %v", err)
		}
		if quit {
			return nil
		}
	}
}

// RunLines runs commands one after another and stops at the first failure,
// returning it (so a script sees a non-zero exit).
func (s *Shell) RunLines(lines []string) error {
	for _, l := range lines {
		quit, err := s.Exec(l)
		if err != nil {
			return err
		}
		if quit {
			return nil
		}
	}
	return nil
}

// ---- state ----

func (s *Shell) load(force bool) error {
	if s.loaded && !force && time.Since(s.at) < time.Second {
		return nil
	}
	st, err := s.game.State(s.ctx)
	if err != nil {
		return err
	}
	s.st, s.loaded, s.at = st, true, time.Now()
	if v := st.Game.Rules.Step; v != "" {
		s.step = v
	}
	s.setMe(st.Me)
	if s.currentCard() == nil { // first time, or the card went away: the lowest card with something left to dig
		for _, c := range s.cards() {
			if c.Opened() < len(c.Cells) {
				s.card = c.ID
				break
			}
		}
		if s.currentCard() == nil && len(s.st.Game.Cards) > 0 {
			s.card = s.cards()[0].ID
		}
	}
	if st.Drained > 0 {
		s.say("你不在的时候苦力怕炸掉了 %s", s.money(st.Drained))
	}
	return nil
}

func (s *Shell) setMe(me Me) {
	s.st.Me = me
	if me.CoolingMs > 0 {
		s.cool = time.Now().Add(time.Duration(me.CoolingMs) * time.Millisecond)
	}
}

func (s *Shell) money(p int) string { return FormatUnits(p, s.step) }

func (s *Shell) cards() []PublicCard {
	cs := append([]PublicCard(nil), s.st.Game.Cards...)
	sort.Slice(cs, func(i, j int) bool { return cs[i].ID < cs[j].ID })
	return cs
}

func (s *Shell) currentCard() *PublicCard {
	for _, c := range s.cards() {
		if c.ID == s.card {
			c := c
			return &c
		}
	}
	return nil
}

func (s *Shell) myUID() int64 { return s.game.MyUID() }

// ---- commands ----

const helpText = `命令（每条只做一个动作）：
  s            状态一行      m [卡号]   文本地图      ls  卡列表      c <卡号>  换卡
  d <行> <列>  挖一格（也可 d <第几格>）            f [n] 动态      g  收益卡    r  刷新
  u <道具> [行 列]  用道具：sg 望远镜  t 火把  ph/pv/px 附魔镐横/竖/十字  k 钥匙
                    map 藏宝图  p 急迫药水  e y 末影箱(要带 y)  tb h|v|x 附魔台
  claim [y]    领取（不带 y 会再问一次）            l  图例
  hint         建议（启动时要加 --hints）：哪些格按规则不可能有危险、其余的概率；只建议，不替你挖
  b            老板键：清屏和滚动缓冲            h  帮助      q  退出`

// Exec runs one command line. quit is true for q. A non-nil error is a
// user-facing failure (printed as "! ..." by Run).
func (s *Shell) Exec(line string) (quit bool, err error) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return false, nil
	}
	cmd, args := strings.ToLower(f[0]), f[1:]
	switch cmd {
	case "q", "quit", "exit":
		return true, nil
	case "h", "help", "?":
		s.say("%s", helpText)
	case "l", "legend":
		s.say("%s", legendFull())
	case "b", "boss":
		fmt.Fprint(s.out, "\x1b[2J\x1b[3J\x1b[H") // clear the screen and the scrollback
	case "s", "status":
		err = s.cmdStatus()
	case "m", "map":
		err = s.cmdMap(args)
	case "ls":
		err = s.cmdList()
	case "c", "card":
		err = s.cmdCard(args)
	case "d", "dig":
		err = s.cmdDig(args)
	case "u", "use":
		err = s.cmdUse(args)
	case "claim":
		err = s.cmdClaim(args)
	case "f", "feed":
		err = s.cmdFeed(args)
	case "g", "summary":
		err = s.cmdSummary()
	case "hint", "x":
		err = s.cmdHint()
	case "r", "refresh":
		if err = s.load(true); err == nil {
			s.say("已刷新")
		}
	default:
		err = fmt.Errorf("不认识的命令 %q（h 看帮助）", cmd)
	}
	return false, err
}

// itemCodes are the short names for the toolbar.
var itemCodes = map[string]string{
	"sg": "spyglass", "spyglass": "spyglass", "t": "torch", "torch": "torch",
	"ph": "pick-h", "pv": "pick-v", "px": "pick-x", "pick-h": "pick-h", "pick-v": "pick-v", "pick-x": "pick-x",
	"k": "key", "key": "key", "map": "map", "p": "potion", "potion": "potion",
	"e": "ender", "ender": "ender", "tb": "table", "table": "table",
}

func codeOf(id string) string {
	for code, v := range itemCodes {
		if v == id && len(code) <= 2 {
			return code
		}
	}
	if id == "map" {
		return "map"
	}
	return id
}

func (s *Shell) cmdStatus() error {
	if err := s.load(true); err != nil {
		return err
	}
	g, me := s.st.Game, s.st.Me
	parts := []string{kv("奖池", s.money(g.Left)+"/"+s.money(g.Pool)), kv("待领", good.Bold(true).Render(s.money(me.Points)))}
	if me.Chest > 0 {
		parts = append(parts, good.Render("箱 +"+s.money(me.Chest)))
	}
	if me.Chances == 0 {
		parts = append(parts, kv("镐", danger.Render("0")))
	} else {
		parts = append(parts, kv("镐", fmt.Sprint(me.Chances)))
	}
	if w := time.Until(s.cool); w > 0 && me.Status != "claimed" {
		parts = append(parts, warn.Render(fmt.Sprintf("冷却 %.1fs", w.Seconds())))
	}
	if me.Poison > 0 {
		parts = append(parts, danger.Render(fmt.Sprintf("中毒%d", me.Poison)))
	}
	if me.Shields > 0 || me.Shield {
		parts = append(parts, dim.Render("盾"))
	}
	switch {
	case g.Over:
		parts = append(parts, dim.Render("已结束"))
	case me.Status == "claimed":
		parts = append(parts, dim.Render("已领取"))
	case g.WrapUpAt != nil && time.Now().UnixMilli() >= *g.WrapUpAt:
		parts = append(parts, danger.Render("收尾中(不能挖了，claim 领取)"))
	case g.DeadlineAt != nil:
		parts = append(parts, dim.Render("剩 "+until(*g.DeadlineAt)))
	}
	s.say("%s", strings.Join(parts, dim.Render(" · ")))
	var items []string
	for _, d := range slots(me) {
		items = append(items, bold.Render(codeOf(d.ID))+dim.Render(fmt.Sprintf("×%d", d.count(me))))
	}
	if len(items) > 0 {
		s.say("%s %s", dim.Render("道具"), strings.Join(items, " "))
	}
	return nil
}

func (s *Shell) cmdList() error {
	if err := s.load(false); err != nil {
		return err
	}
	for _, c := range s.cards() {
		mark := " "
		if c.ID == s.card {
			mark = "*"
		}
		name := "普通"
		if n := BiomeNames[c.Kind]; n != "" {
			name = n
		}
		extra := ""
		if c.Closed {
			extra = " 已关闭"
		}
		s.say("%s#%d %s %dx%d %d/%d%s", mark, c.ID, name, c.Size, c.Size, c.Opened(), len(c.Cells), extra)
	}
	return nil
}

func (s *Shell) cmdCard(args []string) error {
	if len(args) != 1 {
		return errors.New("用法：c <卡号>")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
	if err != nil {
		return errors.New("卡号要是数字")
	}
	if err := s.load(false); err != nil {
		return err
	}
	if s.st.Game.Card(n) == nil {
		return fmt.Errorf("没有卡 #%d（ls 看列表）", n)
	}
	s.card = n
	s.say("当前卡 #%d", n)
	return nil
}

// kindChars is the one Chinese character that stands for a kind on the text
// map. A character (two columns wide in every terminal that shows Chinese) can be
// read without a legend and is never confused with a marker digit; every
// character is different, so the map still makes sense with colour stripped
// (pasted into a chat, say).
var kindChars = []struct {
	k Kind
	c string
}{
	{"emerald", "绿"}, {"diamond", "钻"}, {"apple", "苹"}, {"totem", "镐"}, {"tnt", "炸"}, {"pearl", "珠"},
	{"eye", "眼"}, {"crystal", "晶"}, {"shield", "盾"}, {"spyglass", "镜"}, {"coal", "煤"}, {"stick", "棍"},
	{"map", "图"}, {"potion", "药"}, {"key", "钥"}, {"vault", "库"}, {"deco", "饰"}, {"lava", "岩"},
	{"debris", "骸"}, {"cobweb", "网"}, {"spawner", "笼"}, {"minecart", "车"}, {"ender", "箱"},
}

const (
	unknownCell = ". " // not dug: two columns, like a character
	burntChar   = "岩"  // swallowed by lava
)

func symbolOf(k Kind) string {
	for _, e := range kindChars {
		if e.k == k {
			return e.c
		}
	}
	return "？"
}

// markHelp explains the marker column.
var markHelp = []struct{ m, text string }{
	{"*", "你挖的"}, {"?", "只有你知道（望远镜/藏宝图）"}, {"0", "火把 绿"}, {"1", "火把 黄"},
	{"2", "火把 橙"}, {"3", "火把 红"}, {"+", "零风险（--hints）"},
}

// legendFull is the whole legend, for the `l` command.
func legendFull() string {
	var b strings.Builder
	b.WriteString("图例 每格：一个汉字是东西，后面一位是标记\n  ")
	b.WriteString(strings.TrimSpace(unknownCell) + " 没挖")
	for _, e := range kindChars {
		b.WriteString("  " + e.c + " " + kindName(e.k))
	}
	b.WriteString("\n  标记：")
	for i, h := range markHelp {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(h.m + " " + h.text)
	}
	return b.String()
}

func kindName(k Kind) string {
	if n := KindNames[k]; n != "" {
		return n
	}
	return string(k)
}

// noteSeen remembers what the map shows, so the legend under it lists only that.
func (s *Shell) noteSeen(k Kind, mark string) {
	if k != "" {
		if s.seenKinds == nil {
			s.seenKinds = map[Kind]bool{}
		}
		s.seenKinds[k] = true
	}
	if mark != " " && mark != "" {
		if s.seenMarks == nil {
			s.seenMarks = map[string]bool{}
		}
		s.seenMarks[mark] = true
	}
}

// legendLines is the short legend printed under a map.
func (s *Shell) legendLines() []string {
	var kinds, marks []string
	for _, e := range kindChars {
		if s.seenKinds[e.k] {
			kinds = append(kinds, kindStyle(e.k).Render(e.c)+dim.Render(" "+kindName(e.k)))
		}
	}
	for _, h := range markHelp {
		if s.seenMarks[h.m] {
			marks = append(marks, dim.Render(h.m+" "+h.text))
		}
	}
	var out []string
	if len(kinds) > 0 {
		out = append(out, strings.Join(kinds, dim.Render("  ")))
	}
	if len(marks) > 0 {
		out = append(out, strings.Join(marks, dim.Render("  ")))
	}
	return out
}

// cellText is one cell of the text map: the thing (one character, two columns),
// then a marker (one column).
func (s *Shell) cellText(c PublicCard, idx int, last bool) string {
	ch, mark := unknownCell, " "
	st, markSt := dim, dim
	var shown Kind
	cell := cellAt(c, idx)
	switch {
	case cell != nil && cell.Burn == 1:
		ch, st, shown = burntChar, lipgloss.NewStyle().Foreground(lipgloss.Color("202")), "lava"
	case cell != nil:
		ch, st, shown = symbolOf(cell.Kind), kindStyle(cell.Kind), cell.Kind
		if cell.UID == s.myUID() { // what you dug yourself: bold, with a gold *
			mark, markSt = "*", lipgloss.NewStyle().Foreground(lipgloss.Color("178"))
			st = st.Bold(true)
		}
	case c.Fixed[fmt.Sprint(idx)] != "":
		k := Kind(c.Fixed[fmt.Sprint(idx)])
		ch, st, shown = symbolOf(k), kindStyle(k), k
	default:
		if k, ok := peekIn(s.st.Me.Peeks, c.ID, idx); ok { // only you know this one
			peek := lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
			ch, mark, st, markSt, shown = symbolOf(k), "?", peek, peek, k
		} else if s.mapHint != nil && s.mapHint.IsSafe(idx) { // the advisor: cannot hold a danger
			mark, markSt = "+", good.Bold(true)
		}
	}
	if t, ok := c.torchAt(idx); ok {
		mark = string(rune('0' + min(t.Level, 3)))
		markSt = lipgloss.NewStyle().Foreground(lipgloss.Color(torchColor(t.Level))).Bold(true)
	}
	s.noteSeen(shown, mark)
	if !last { // a space between cells
		return st.Render(ch) + markSt.Render(mark) + " "
	}
	if mark == " " { // no trailing space at the end of a row, with or without colour
		return st.Render(strings.TrimRight(ch, " "))
	}
	return st.Render(ch) + markSt.Render(mark)
}

// kindStyle colours a kind as the game does (the same colours as the full-screen mode).
func kindStyle(k Kind) lipgloss.Style {
	if g, ok := glyphs[k]; ok {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(g.color))
	}
	return dim
}

// kv is a dim label with a plain value.
func kv(label, value string) string { return dim.Render(label+" ") + value }

func peekIn(peeks []Peek, card, idx int) (Kind, bool) {
	for _, p := range peeks {
		if p.CardID == card && p.Index == idx {
			return p.Kind, true
		}
	}
	return "", false
}

func (s *Shell) cmdMap(args []string) error {
	if err := s.load(true); err != nil {
		return err
	}
	c := s.currentCard()
	if len(args) == 1 {
		n, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
		if err != nil || s.st.Game.Card(n) == nil {
			return fmt.Errorf("没有卡 %s（ls 看列表）", args[0])
		}
		cc := *s.st.Game.Card(n)
		c = &cc
	}
	if c == nil {
		return errors.New("还没有地图")
	}
	name := "普通"
	if n := BiomeNames[c.Kind]; n != "" {
		name = n
	}
	extra := ""
	switch {
	case c.Closed:
		extra = " 已关闭(监守者醒了)"
	case c.Kind == "deep":
		extra = fmt.Sprintf(" 噪音%d", int(c.Noise))
	}
	title := lipgloss.NewStyle().Bold(true)
	if col := biomeColor[c.Kind]; col != "" {
		title = title.Foreground(lipgloss.Color(col))
	}
	s.say("%s %s%s", title.Render(fmt.Sprintf("#%d %s", c.ID, name)), dim.Render(fmt.Sprintf("%dx%d %d/%d", c.Size, c.Size, c.Opened(), len(c.Cells))), warn.Render(extra))
	var head strings.Builder
	head.WriteString("  ")
	for x := 1; x <= c.Size; x++ {
		fmt.Fprintf(&head, "%-4d", x) // a cell is four columns: character, marker, a space
	}
	s.say("%s", dim.Render(strings.TrimRight(head.String(), " ")))
	s.seenKinds, s.seenMarks = nil, nil
	if s.Hints {
		a := Advise(*c, s.st.Me.Peeks)
		s.mapHint = &a
		defer func() { s.mapHint = nil }()
	}
	for y := 0; y < c.Size; y++ {
		var row strings.Builder
		row.WriteString(dim.Render(fmt.Sprintf("%d ", y+1)))
		for x := 0; x < c.Size; x++ {
			row.WriteString(s.cellText(*c, y*c.Size+x, x == c.Size-1))
		}
		s.say("%s", strings.TrimRight(row.String(), " "))
	}
	for _, l := range s.legendLines() {
		s.say("%s", l)
	}
	return nil
}

// cellArg turns "r c" or "n" (both 1-based) into a cell index of card c.
func cellArg(args []string, c PublicCard) (int, error) {
	n := c.Size * c.Size
	switch len(args) {
	case 1:
		v, err := strconv.Atoi(args[0])
		if err != nil || v < 1 || v > n {
			return 0, fmt.Errorf("格子号要在 1..%d", n)
		}
		return v - 1, nil
	case 2:
		r, e1 := strconv.Atoi(args[0])
		col, e2 := strconv.Atoi(args[1])
		if e1 != nil || e2 != nil || r < 1 || r > c.Size || col < 1 || col > c.Size {
			return 0, fmt.Errorf("行和列都要在 1..%d", c.Size)
		}
		return (r-1)*c.Size + (col - 1), nil
	}
	return 0, errors.New("要写位置：<行> <列>，或 <第几格>")
}

func (s *Shell) wrapUp() bool {
	w := s.st.Game.WrapUpAt
	return w != nil && time.Now().UnixMilli() >= *w
}

func (s *Shell) cmdDig(args []string) error {
	if err := s.load(false); err != nil {
		return err
	}
	c := s.currentCard()
	if c == nil {
		return errors.New("还没有地图")
	}
	idx, err := cellArg(args, *c)
	if err != nil {
		return err
	}
	switch {
	case s.st.Game.Over:
		return errors.New("已经结束了")
	case s.st.Me.Status == "claimed":
		return errors.New("你已经领取过了，不能再挖")
	case s.wrapUp():
		return errors.New("已进入收尾，不能再挖了；claim 领取")
	case c.Closed:
		return errors.New("这张卡关闭了（监守者醒了）")
	case c.Fixed[fmt.Sprint(idx)] != "":
		return errors.New("这一格不能挖")
	case cellAt(*c, idx) != nil:
		return errors.New("这一格已经挖开了")
	}
	if w := time.Until(s.cool); w > 0 {
		return fmt.Errorf("还要等 %.1f 秒才能再挖", w.Seconds())
	}
	var guess *HintRecord
	if s.HintLog != nil {
		if r, ok := predict(*c, s.st.Me.Peeks, idx, s.Packet); ok {
			guess = &r
		}
	}
	ans, err := s.game.Dig(s.ctx, c.ID, idx)
	if err != nil {
		return err
	}
	if guess != nil && ans.OK && ans.Result != nil {
		s.HintLog.Record(finish(*guess, ans.Result))
	}
	s.setMe(ans.Me)
	if !ans.OK {
		if ans.Card != nil {
			s.st.Game.put(*ans.Card)
		}
		return errors.New(firstNonEmpty(ans.Message, "没挖成"))
	}
	if len(ans.Update) > 0 {
		var l Live
		if json.Unmarshal(ans.Update, &l) == nil {
			s.st.Game.Seq = max(s.st.Game.Seq, 0)
			s.st.Apply(l)
		}
	}
	amount := ""
	text := "挖开了"
	if r := ans.Result; r != nil {
		text = firstNonEmpty(r.Text, text)
		switch {
		case r.Amount > 0:
			amount = " " + good.Bold(true).Render("+"+s.money(r.Amount))
		case r.Amount < 0:
			amount = " " + danger.Render(s.money(r.Amount))
		}
	}
	sep := dim.Render("|")
	s.say("%s%s %s %s %s %s", text, amount, sep, kv("待领", s.money(s.st.Me.Points)), sep, kv("镐", fmt.Sprint(s.st.Me.Chances)))
	if ans.ClaimNow {
		s.say("%s", warn.Render("镐用完了：claim 领取"))
	}
	return nil
}

func (s *Shell) cmdUse(args []string) error {
	if len(args) == 0 {
		return errors.New("用法：u <道具> [行 列]（h 看道具名）")
	}
	id, ok := itemCodes[strings.ToLower(args[0])]
	if !ok {
		return fmt.Errorf("不认识的道具 %q（h 看道具名）", args[0])
	}
	if err := s.load(false); err != nil {
		return err
	}
	var def itemDef
	for _, d := range itemDefs {
		if d.ID == id {
			def = d
		}
	}
	if def.count(s.st.Me) <= 0 {
		return fmt.Errorf("你没有%s", def.Name)
	}
	rest := args[1:]
	cardID, index, choice := -1, -1, ""
	switch def.How {
	case useAim:
		c := s.currentCard()
		if c == nil {
			return errors.New("还没有地图")
		}
		idx, err := cellArg(rest, *c)
		if err != nil {
			return err
		}
		if why := aimProblem(def, c, idx, s.st.Game.Rules); why != "" {
			return errors.New(why)
		}
		cardID, index = c.ID, idx
	case useConfirm:
		if id == "ender" && (len(rest) == 0 || strings.ToLower(rest[0]) != "y") {
			return errors.New("末影箱会把手上的分一半存起来、一半毁掉；确定就写 u e y")
		}
	case useChoose:
		if len(rest) != 1 {
			return fmt.Errorf("用法：u tb <形状>，可选 %s", strings.Join(s.st.Me.TableChoices, "/"))
		}
		choice = strings.ToLower(rest[0])
		offered := false
		for _, ch := range s.st.Me.TableChoices {
			offered = offered || ch == choice
		}
		if !offered {
			return fmt.Errorf("只能选服务器给出的：%s", strings.Join(s.st.Me.TableChoices, "/"))
		}
	}
	ans, err := s.game.Use(s.ctx, def.ID, cardID, index, choice)
	if err != nil {
		return err
	}
	s.setMe(ans.Me)
	if !ans.OK {
		if ans.Card != nil {
			s.st.Game.put(*ans.Card)
		}
		return errors.New(firstNonEmpty(ans.Message, "没用成"))
	}
	for _, u := range ans.Updates {
		var l Live
		if json.Unmarshal(u, &l) == nil {
			s.st.Apply(l)
		}
	}
	if ans.Card != nil {
		s.st.Game.put(*ans.Card)
	}
	if ans.Left != nil {
		s.st.Game.Left = *ans.Left
	}
	line := def.Name + "：" + firstNonEmpty(ans.Text, "用掉了")
	if n := len(ans.Results); n > 1 {
		total := 0
		for _, r := range ans.Results {
			total += r.Amount
		}
		line += fmt.Sprintf("（挖开 %d 格，合计 %s）", n, s.money(total))
	}
	if p := ans.Peek; p != nil {
		s.card = p.CardID
		line += fmt.Sprintf(" → 卡#%d 第%d行第%d列是%s", p.CardID, p.Index/s.sizeOf(p.CardID)+1, p.Index%s.sizeOf(p.CardID)+1, KindNames[p.Kind])
	}
	s.say("%s", line)
	if ans.ClaimNow {
		s.say("镐用完了：claim 领取")
	}
	return nil
}

func (s *Shell) sizeOf(card int) int {
	if c := s.st.Game.Card(card); c != nil && c.Size > 0 {
		return c.Size
	}
	return 1
}

func (s *Shell) cmdClaim(args []string) error {
	if err := s.load(true); err != nil {
		return err
	}
	if s.st.Me.Status == "claimed" {
		return errors.New("你已经领取过了")
	}
	total := s.st.Me.Points + s.st.Me.Chest
	if !(len(args) == 1 && strings.EqualFold(args[0], "y")) {
		fmt.Fprintf(s.out, "现在领取 %s？领取后不能再挖。输入 y 确认: ", s.money(total))
		if !s.scan.Scan() || !strings.EqualFold(strings.TrimSpace(s.scan.Text()), "y") {
			s.say("")
			return errors.New("没有领取")
		}
	}
	ans, err := s.game.Claim(s.ctx)
	if err != nil {
		return err
	}
	if !ans.OK {
		return errors.New("领取失败：" + firstNonEmpty(ans.Message, "未知原因"))
	}
	s.setMe(ans.Me)
	s.say("已领取 %s，交给红包站结算…", s.money(ans.Points))
	line, err := s.submit(s.ctx, ans.Voucher)
	if err != nil {
		return fmt.Errorf("结算没交上去：%v\n凭证（请保存，可在红包页的指令框里提交）：%s", err, ans.Voucher)
	}
	s.say("%s", line)
	return nil
}

// cmdHint says where the dangers cannot be, and the odds elsewhere. Advice
// only: it never digs, and it needs --hints.
func (s *Shell) cmdHint() error {
	if !s.Hints {
		return errors.New("建议功能没开：启动时加 --hints")
	}
	if err := s.load(true); err != nil {
		return err
	}
	c := s.currentCard()
	if c == nil {
		return errors.New("还没有地图")
	}
	a := Advise(*c, s.st.Me.Peeks)
	if len(a.Risk) == 0 && len(a.Safe) == 0 {
		s.say("%s", dim.Render(firstNonEmpty(strings.Join(a.Notes, "；"), "没有可用的信息")))
		return nil
	}
	s.say("%s %s", bold.Render(fmt.Sprintf("#%d", c.ID)), dim.Render(fmt.Sprintf("还藏着 %d 个危险在 %d 格里（估算）", a.Dangers, a.Unknown)))
	var safe []string
	for _, i := range a.Safe {
		if cellAt(*c, i) == nil {
			safe = append(safe, coord(c.Size, i))
		}
	}
	if len(safe) > 0 {
		s.say("%s %s", good.Render("零风险"), strings.Join(safe, " "))
	} else {
		s.say("%s", dim.Render("没有确定零风险的格"))
	}
	if low := a.Lowest(5); len(low) > 0 {
		var ps []string
		for _, p := range low {
			ps = append(ps, fmt.Sprintf("%s %.0f%%", coord(c.Size, p.Index), p.Risk*100))
		}
		s.say("%s %s", warn.Render("最低"), strings.Join(ps, dim.Render(" · ")))
	}
	if !a.Exact {
		s.say("%s", dim.Render("（有些条件没用上，仅供参考）"))
	}
	return nil
}

func (s *Shell) cmdFeed(args []string) error {
	if err := s.load(true); err != nil {
		return err
	}
	n := 8
	if len(args) == 1 {
		if v, err := strconv.Atoi(args[0]); err == nil && v > 0 {
			n = v
		}
	}
	feed := s.st.Game.Feed
	if len(feed) > n {
		feed = feed[:n]
	}
	if len(feed) == 0 {
		s.say("还没有人挖")
	}
	for i := len(feed) - 1; i >= 0; i-- { // oldest first, so the newest ends up at the bottom
		f := feed[i]
		text := f.Text
		if f.Name != "" && !strings.HasPrefix(text, f.Name) {
			text = f.Name + " " + text
		}
		s.say("%s", text)
	}
	return nil
}

func (s *Shell) cmdSummary() error {
	sum, err := s.game.Summary(s.ctx)
	if err != nil {
		return err
	}
	if err := s.load(false); err != nil {
		return err
	}
	s.say("%s：待领 %s · 挖了 %d 格 · 最大一笔 %s · 踩 TNT %d 次 · 盾牌挡 %d 次 · 被扣 %s",
		sum.Me.Name, s.money(sum.Me.Points), sum.Me.Digs, s.money(sum.Me.Best), sum.Me.TNT, sum.Me.Blocked, s.money(sum.Me.Lost))
	for i, t := range sum.Top {
		if i >= 5 {
			break
		}
		me := ""
		if t.UID == s.myUID() {
			me = " ←你"
		}
		s.say("%d. %s %s%s", i+1, t.Name, s.money(t.Points), me)
	}
	return nil
}
