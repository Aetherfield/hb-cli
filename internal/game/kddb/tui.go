package kddb

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"hb/internal/assets"
)

// Game is what the terminal UI needs from a game connection (Client implements it).
type Game interface {
	State(ctx context.Context) (State, error)
	Dig(ctx context.Context, cardID, index int) (DigAnswer, error)
	Use(ctx context.Context, item string, cardID, index int, choice string) (UseAnswer, error)
	Summary(ctx context.Context) (Summary, error)
	Claim(ctx context.Context) (ClaimAnswer, error)
	Live(ctx context.Context, onMsg func(Live)) error
	MyUID() int64
}

// SubmitFunc hands a claim voucher to lafish's judge and returns a line for the player.
type SubmitFunc func(ctx context.Context, voucher string) (string, error)

type glyph struct {
	ch    string
	color string
}

var glyphs = map[Kind]glyph{
	"emerald": {"◆", "10"}, "diamond": {"◇", "14"}, "apple": {"♥", "11"}, "totem": {"T", "11"},
	"tnt": {"✹", "9"}, "pearl": {"●", "13"}, "shield": {"◈", "12"}, "spyglass": {"◎", "14"},
	"eye": {"◉", "13"}, "crystal": {"✦", "9"}, "coal": {"▪", "8"}, "stick": {"/", "3"},
	"map": {"▤", "11"}, "potion": {"♨", "13"}, "key": {"k", "11"}, "vault": {"▣", "11"},
	"deco": {"▓", "8"}, "lava": {"≈", "202"}, "debris": {"▩", "5"}, "cobweb": {"※", "7"},
	"spawner": {"⌘", "9"}, "minecart": {"▭", "7"}, "ender": {"◘", "13"},
}

// What the keyboard is doing right now.
type mode int

const (
	modeDig     mode = iota // arrows move, space digs
	modeAim                 // an item is armed; enter uses it on the cursor cell
	modeConfirm             // y/n
	modeChoose              // pick an enchanting-table shape
	modeSummary             // the 收益卡 overlay
	modeHelp
)

type (
	stateMsg struct {
		st  State
		err error
	}
	digMsg struct {
		ans DigAnswer
		err error
	}
	useMsg struct {
		ans UseAnswer
		err error
		def itemDef
	}
	claimMsg struct {
		ans ClaimAnswer
		err error
	}
	submitMsg struct {
		line string
		err  error
	}
	summaryMsg struct {
		sum Summary
		err error
	}
	liveMsg Live
	tickMsg time.Time
)

// Options tune the terminal game.
type Options struct {
	// AutoClaim claims for you when your chances are used up or the wrap-up
	// starts, as the web page does (unclaimed points are void at the deadline).
	// It never digs.
	AutoClaim bool
	// Render is "auto" (pixel icons when the terminal has true colour and room),
	// "glyph" (always symbols) or "sprite" (always pixel icons).
	Render string
	// TrueColor says the terminal shows 24-bit colour; "auto" needs it.
	TrueColor bool
	// Pack holds Minecraft's textures if the player fetched them (hb assets
	// fetch); nil means hb draws its own icons.
	Pack *assets.Pack
	// Hints turns on the advisor (advisor.go): it marks cells that cannot hold a
	// danger and estimates the odds of the rest, from what the game shows you.
	// It only advises; every dig is still yours. Off unless asked for.
	Hints bool
	// HintLog, if set, records the advisor's guess for each cell you dig and what
	// it turned out to be, so its accuracy can be checked (hb hints report).
	HintLog HintLog
	// Packet is the packet's id, written into that log.
	Packet string
}

// Model is the bubbletea model for one packet's game.
type Model struct {
	game   Game
	submit SubmitFunc
	opts   Options
	step   string // smallest unit, for money
	ctx    context.Context
	live   chan Live

	st       State
	loaded   bool
	card     int // index into the sorted cards
	cur      int // cell index on the card
	busy     bool
	cool     time.Time
	creeper  time.Time // when the creeper blows, if one is coming
	notice   string
	mode     mode
	aim      *itemDef
	confirm  *confirmAction
	summary  *Summary
	voucher  string
	claiming bool
	claimNow bool
	err      error
	width    int
	height   int

	zoom    int // 0 auto, -1 symbols, else a fixed icon size in pixels (4, 6, 8)
	masters map[Kind]grid
	icons   map[iconKey]grid
	stones  map[stoneKey]grid

	hintsOn  bool // the advisor's marks are shown (only if opts.Hints)
	adv      adviceCache
	pending  *HintRecord // the guess for the dig that is in flight
	autoJump bool        // after a dig, the cursor moves to the nearest cell with no danger (it never digs)

	hit     hitMap    // where the clickable things are on the screen, as of the last View
	gridTop int       // lines above the card's grid inside its panel
	world   worldView // the map window
	drag    dragState // a mouse button held down
}

type confirmAction struct {
	label string
	run   func() tea.Msg
}

// NewModel builds the model. ctx stops the live connection when it ends.
func NewModel(ctx context.Context, g Game, submit SubmitFunc, opts Options) *Model {
	return &Model{game: g, submit: submit, opts: opts, ctx: ctx, live: make(chan Live, 64), step: "0.01", width: 100, hintsOn: opts.Hints, autoJump: opts.Hints}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.load(), m.startLive(), tick(), m.waitLive())
}

func (m *Model) load() tea.Cmd {
	return func() tea.Msg {
		st, err := m.game.State(m.ctx)
		return stateMsg{st, err}
	}
}

// startLive keeps one push connection alive, reconnecting with backoff, and
// asks for a reload after each reconnect to catch up on missed messages.
func (m *Model) startLive() tea.Cmd {
	return func() tea.Msg {
		go func() {
			delay := time.Second
			for m.ctx.Err() == nil {
				started := time.Now()
				_ = m.game.Live(m.ctx, func(l Live) {
					select {
					case m.live <- l:
					default: // UI is behind; the reload below repairs it
					}
				})
				if m.ctx.Err() != nil {
					return
				}
				select {
				case m.live <- Live{Type: "_reconnect"}:
				default:
				}
				if time.Since(started) > 30*time.Second {
					delay = time.Second
				}
				select {
				case <-time.After(delay):
				case <-m.ctx.Done():
					return
				}
				if delay < 30*time.Second {
					delay *= 2
				}
			}
		}()
		return nil
	}
}

func (m *Model) waitLive() tea.Cmd {
	return func() tea.Msg {
		select {
		case l := <-m.live:
			return liveMsg(l)
		case <-m.ctx.Done():
			return nil
		}
	}
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) cards() []PublicCard {
	cs := append([]PublicCard(nil), m.st.Game.Cards...)
	sort.Slice(cs, func(i, j int) bool { return cs[i].ID < cs[j].ID })
	return cs
}

func (m *Model) current() *PublicCard {
	cs := m.cards()
	if len(cs) == 0 {
		return nil
	}
	if m.card >= len(cs) {
		m.card = len(cs) - 1
	}
	c := cs[m.card]
	return &c
}

func (m *Model) selectCard(id int) {
	for i, c := range m.cards() {
		if c.ID == id {
			m.card = i
			return
		}
	}
}

func (m *Model) setMe(me Me) {
	m.st.Me = me
	if me.CoolingMs > 0 {
		m.cool = time.Now().Add(time.Duration(me.CoolingMs) * time.Millisecond)
	}
	if me.CreeperMs != nil {
		m.creeper = time.Now().Add(time.Duration(*me.CreeperMs) * time.Millisecond)
	} else {
		m.creeper = time.Time{}
	}
}

func (m *Model) money(points int) string { return FormatUnits(points, m.step) }

// wrapUpStarted: the last minutes, when digging stops and the page claims for you.
func (m *Model) wrapUpStarted() bool {
	w := m.st.Game.WrapUpAt
	return w != nil && time.Now().UnixMilli() >= *w
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		return m, tea.Batch(tick(), m.maybeAutoClaim())
	case stateMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err, m.loaded, m.st = nil, true, msg.st
		if s := msg.st.Game.Rules.Step; s != "" {
			m.step = s
		}
		m.setMe(msg.st.Me)
		if msg.st.Drained > 0 {
			m.notice = fmt.Sprintf("你不在的时候苦力怕炸掉了 %s", m.money(msg.st.Drained))
		}
	case liveMsg:
		l := Live(msg)
		if l.Type == "_reconnect" {
			return m, tea.Batch(m.load(), m.waitLive())
		}
		if !m.st.Apply(l) {
			return m, tea.Batch(m.load(), m.waitLive())
		}
		return m, m.waitLive()
	case digMsg:
		m.busy = false
		m.recordOutcome(msg)
		if msg.err != nil {
			m.notice = "挖不了：" + msg.err.Error()
			return m, nil
		}
		m.setMe(msg.ans.Me)
		if !msg.ans.OK {
			m.notice = msg.ans.Message
			if msg.ans.Card != nil {
				m.st.Game.put(*msg.ans.Card)
			}
			return m, nil
		}
		if len(msg.ans.Update) > 0 {
			if cmd := m.applyRaw(msg.ans.Update); cmd != nil {
				return m, cmd
			}
		}
		if r := msg.ans.Result; r != nil {
			m.notice = r.Text
		}
		if msg.ans.ClaimNow {
			m.claimNow = true
			m.notice += "  （机会用完了，按 c 领取）"
		}
		m.jumpNearestSafe()
	case useMsg:
		m.busy = false
		if msg.err != nil {
			m.notice = "用不了：" + msg.err.Error()
			return m, nil
		}
		m.setMe(msg.ans.Me)
		if !msg.ans.OK {
			m.notice = msg.ans.Message
			if msg.ans.Card != nil {
				m.st.Game.put(*msg.ans.Card)
			}
			return m, nil
		}
		m.mode, m.aim = modeDig, nil
		for _, u := range msg.ans.Updates {
			if cmd := m.applyRaw(u); cmd != nil {
				return m, cmd
			}
		}
		if msg.ans.Card != nil {
			m.st.Game.put(*msg.ans.Card)
		}
		if msg.ans.Left != nil {
			m.st.Game.Left = *msg.ans.Left
		}
		m.notice = msg.def.Name + "：" + firstNonEmpty(msg.ans.Text, "用掉了")
		if len(msg.ans.Results) > 1 {
			total := 0
			for _, r := range msg.ans.Results {
				total += r.Amount
			}
			m.notice += fmt.Sprintf("（挖开 %d 格，合计 %s）", len(msg.ans.Results), m.money(total))
		}
		if p := msg.ans.Peek; p != nil { // the treasure map or spyglass: show where
			m.selectCard(p.CardID)
			m.cur = p.Index
			m.notice += fmt.Sprintf("  → 卡 #%d 第 %d 格是%s", p.CardID, p.Index+1, KindNames[p.Kind])
		}
		if msg.ans.ClaimNow {
			m.claimNow = true
			m.notice += "  （机会用完了，按 c 领取）"
		}
	case summaryMsg:
		m.busy = false
		if msg.err != nil {
			m.notice = "看不了收益卡：" + msg.err.Error()
			return m, nil
		}
		m.summary, m.mode = &msg.sum, modeSummary
	case claimMsg:
		m.busy = false
		if msg.err != nil || !msg.ans.OK {
			m.claiming = false
			m.notice = "领取失败：" + errText(msg.err, msg.ans.Message)
			return m, nil
		}
		m.setMe(msg.ans.Me)
		m.voucher = msg.ans.Voucher
		m.notice = "已领取 " + m.money(msg.ans.Points) + "，正在交给红包站结算…"
		m.busy = true
		return m, func() tea.Msg {
			line, err := m.submit(m.ctx, msg.ans.Voucher)
			return submitMsg{line, err}
		}
	case submitMsg:
		m.busy = false
		if msg.err != nil {
			m.notice = "结算没交上去：" + msg.err.Error()
			if m.voucher != "" {
				m.notice += "\n凭证（请保存，可在红包页的指令框里提交）：" + m.voucher
			}
			return m, nil
		}
		m.notice = msg.line
	case tea.MouseMsg:
		return m.mouse(msg)
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// applyRaw merges one pushed step that came back inside an answer; it asks for
// a reload when the sequence shows a gap.
func (m *Model) applyRaw(raw json.RawMessage) tea.Cmd {
	var l Live
	if json.Unmarshal(raw, &l) != nil {
		return nil
	}
	if !m.st.Apply(l) {
		return m.load()
	}
	return nil
}

// maybeAutoClaim claims what you already earned, but only when you opted in.
func (m *Model) maybeAutoClaim() tea.Cmd {
	if !m.opts.AutoClaim || !m.loaded || m.claiming || m.busy || m.st.Me.Status == "claimed" {
		return nil
	}
	if !(m.claimNow || m.wrapUpStarted()) {
		return nil
	}
	m.claiming, m.busy = true, true
	m.notice = "自动领取（--auto-claim）…"
	return m.doClaim()
}

func (m *Model) doClaim() tea.Cmd {
	return func() tea.Msg {
		a, err := m.game.Claim(m.ctx)
		return claimMsg{a, err}
	}
}

func errText(err error, fallback string) string {
	if err != nil {
		return err.Error()
	}
	return fallback
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// ---- keys ----

func (m *Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeSummary, modeHelp:
		m.mode = modeDig
		return m, nil
	case modeConfirm:
		a := m.confirm
		m.mode, m.confirm = modeDig, nil
		if s == "y" || s == "Y" {
			m.busy = true
			m.notice = "…"
			return m, func() tea.Msg { return a.run() }
		}
		m.notice = "取消了。"
		return m, nil
	case modeChoose:
		return m.chooseShape(s)
	}

	switch s {
	case "q":
		return m, tea.Quit
	case "esc":
		if m.mode == modeAim {
			m.mode, m.aim = modeDig, nil
			m.notice = "收起了，回到镐子。"
			return m, nil
		}
		return m, tea.Quit
	case "r":
		return m, m.load()
	case "?":
		m.mode = modeHelp
		return m, nil
	case "H": // show or hide the advisor (only when it was asked for with --hints)
		if !m.opts.Hints {
			m.notice = "建议功能没开：启动时加 --hints"
			return m, nil
		}
		m.hintsOn = !m.hintsOn
		m.notice = map[bool]string{true: "建议：开", false: "建议：关"}[m.hintsOn]
		return m, nil
	case "J": // after a dig, the cursor goes to the nearest cell with no danger
		if !m.opts.Hints {
			m.notice = "建议功能没开：启动时加 --hints"
			return m, nil
		}
		m.autoJump = !m.autoJump
		m.notice = map[bool]string{true: "挖完光标自动跳到零风险的格子（只移动光标，不会替你挖）", false: "挖完光标留在原地"}[m.autoJump]
		return m, nil
	case "z": // zoom: auto -> symbols -> 4 -> 6 -> 8 -> 16 px icons -> auto
		m.zoom = nextZoom(m.zoom)
		m.notice = "显示：" + m.zoomName()
		return m, nil
	}
	if !m.loaded || m.busy {
		return m, nil
	}
	c := m.current()
	switch s {
	case "left", "h":
		m.move(c, -1, 0)
	case "right", "l":
		m.move(c, 1, 0)
	case "up", "k":
		m.move(c, 0, -1)
	case "down", "j":
		m.move(c, 0, 1)
	case "n":
		m.jumpSafe(1)
	case "N":
		m.jumpSafe(-1)
	case "[", "shift+tab", "pgup":
		m.stepCard(-1)
	case "]", "tab", "pgdown":
		m.stepCard(1)
	case "g":
		m.busy = true
		return m, func() tea.Msg { s, err := m.game.Summary(m.ctx); return summaryMsg{s, err} }
	case "c":
		if m.st.Me.Status == "claimed" {
			m.notice = "你已经领取过了。"
			return m, nil
		}
		m.mode = modeConfirm
		m.notice = fmt.Sprintf("现在领取 %s？领取后不能再挖。 y 确认 / 其他键取消", m.money(m.st.Me.Points+m.st.Me.Chest))
		m.confirm = &confirmAction{label: "claim", run: func() tea.Msg { a, err := m.game.Claim(m.ctx); return claimMsg{a, err} }}
	case "0":
		m.mode, m.aim = modeDig, nil
		m.notice = "拿起镐子。"
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		return m.armSlot(int(s[0] - '0'))
	case " ", "enter":
		return m.act(c)
	}
	return m, nil
}

// act: enter/space does the armed thing, or digs.
func (m *Model) act(c *PublicCard) (tea.Model, tea.Cmd) {
	if c == nil || m.st.Game.Over || m.st.Me.Status == "claimed" {
		return m, nil
	}
	if m.mode == modeAim && m.aim != nil {
		d := *m.aim
		if why := aimProblem(d, c, m.cur, m.st.Game.Rules); why != "" {
			m.notice = why
			return m, nil
		}
		m.busy = true
		cardID, idx := c.ID, m.cur
		return m, func() tea.Msg {
			a, err := m.game.Use(m.ctx, d.ID, cardID, idx, "")
			return useMsg{a, err, d}
		}
	}
	if m.wrapUpStarted() {
		m.notice = "已经进入收尾，不能再挖了；按 c 领取。"
		return m, nil
	}
	if wait := time.Until(m.cool); wait > 0 {
		m.notice = fmt.Sprintf("还要等 %.1f 秒才能再挖", wait.Seconds())
		return m, nil
	}
	if m.cur < 0 || m.cur >= len(c.Cells) || c.Cells[m.cur] != nil || c.Fixed[fmt.Sprint(m.cur)] != "" || c.Closed {
		m.notice = "这一格不能挖。"
		return m, nil
	}
	m.notePrediction(*c, m.cur)
	m.busy = true
	cardID, idx := c.ID, m.cur
	return m, func() tea.Msg {
		a, err := m.game.Dig(m.ctx, cardID, idx)
		return digMsg{a, err}
	}
}

// armSlot picks toolbar slot n (1-based).
func (m *Model) armSlot(n int) (tea.Model, tea.Cmd) {
	sl := slots(m.st.Me)
	if n < 1 || n > len(sl) {
		m.notice = "没有这个道具栏位（按 ? 看说明）。"
		return m, nil
	}
	d := sl[n-1]
	switch d.How {
	case useAim:
		m.mode, m.aim = modeAim, &d
		m.notice = fmt.Sprintf("瞄准 %s：移动到目标格，回车使用，Esc 取消。%s", d.Name, d.Help)
	case useConfirm:
		m.mode = modeConfirm
		m.notice = fmt.Sprintf("使用 %s？%s  y 确认 / 其他键取消", d.Name, d.Help)
		m.confirm = &confirmAction{label: d.ID, run: func() tea.Msg {
			a, err := m.game.Use(m.ctx, d.ID, -1, -1, "")
			return useMsg{a, err, d}
		}}
	case useChoose:
		if len(m.st.Me.TableChoices) == 0 {
			m.notice = "附魔台还没给出可选的两种，先刷新一下（r）。"
			return m, nil
		}
		m.mode = modeChoose
		var opts string
		for _, ch := range m.st.Me.TableChoices {
			opts += fmt.Sprintf("  %s=%s", ch, shapeNames[ch])
		}
		m.notice = "附魔台：选一种附魔镐（得到 2 次）：" + opts + "   其他键取消"
	}
	return m, nil
}

func (m *Model) chooseShape(s string) (tea.Model, tea.Cmd) {
	m.mode = modeDig
	for _, ch := range m.st.Me.TableChoices {
		if s == ch {
			d := itemDef{ID: "table", Name: "附魔台"}
			m.busy = true
			return m, func() tea.Msg {
				a, err := m.game.Use(m.ctx, "table", -1, -1, ch)
				return useMsg{a, err, d}
			}
		}
	}
	m.notice = "取消了。"
	return m, nil
}

func (m *Model) move(c *PublicCard, dx, dy int) {
	if c == nil || c.Size == 0 {
		return
	}
	x, y := m.cur%c.Size+dx, m.cur/c.Size+dy
	if x >= 0 && y >= 0 && x < c.Size && y < c.Size {
		m.cur = y*c.Size + x
		return
	}
	// off the edge: walk on to the neighbouring card in that direction, if any
	m.crossEdge(c, dx, dy)
}

func (m *Model) crossEdge(c *PublicCard, dx, dy int) {
	cx, cy := float64(c.X)+float64(c.Size)/2, float64(c.Y)+float64(c.Size)/2
	row, col := m.cur/c.Size, m.cur%c.Size
	best, bestD := -1, math.MaxFloat64
	for i, o := range m.cards() {
		if o.ID == c.ID {
			continue
		}
		ox, oy := float64(o.X)+float64(o.Size)/2, float64(o.Y)+float64(o.Size)/2
		ddx, ddy := ox-cx, oy-cy
		if (dx > 0 && ddx <= 0) || (dx < 0 && ddx >= 0) || (dy > 0 && ddy <= 0) || (dy < 0 && ddy >= 0) {
			continue
		}
		// distance along the move direction, penalising sideways drift
		d := math.Abs(ddx)*float64(abs(dx)+1) + math.Abs(ddy)*float64(abs(dy)+1)
		if d < bestD {
			best, bestD = i, d
		}
	}
	if best < 0 {
		return
	}
	o := m.cards()[best]
	m.card = best
	switch {
	case dx > 0:
		col = 0
	case dx < 0:
		col = o.Size - 1
	}
	switch {
	case dy > 0:
		row = 0
	case dy < 0:
		row = o.Size - 1
	}
	m.cur = min(row, o.Size-1)*o.Size + min(col, o.Size-1)
}

func nextZoom(z int) int {
	switch z {
	case 0:
		return -1
	case -1:
		return 4
	case 4:
		return 6
	case 6:
		return 8
	case 8:
		return 16
	}
	return 0
}

func (m *Model) zoomName() string {
	switch {
	case m.zoom == -1:
		return "符号"
	case m.zoom > 0:
		return fmt.Sprintf("像素图标 %d 像素", m.zoom)
	}
	t := m.spriteSize(m.current())
	if t == 0 {
		return "自动（当前用符号）"
	}
	return fmt.Sprintf("自动（当前 %d 像素图标）", t)
}

// spriteSize is the icon size to draw the card with, in pixels; 0 means the
// symbol grid.
func (m *Model) spriteSize(c *PublicCard) int {
	if c == nil {
		return 0
	}
	switch {
	case m.zoom == -1:
		return 0
	case m.zoom > 0:
		return m.zoom
	case m.opts.Render == "glyph":
		return 0
	case m.opts.Render == "sprite":
		if t := pickSprite(m.width, m.height, c.Size); t > 0 {
			return t
		}
		return 4
	case m.opts.TrueColor:
		return pickSprite(m.width, m.height, c.Size)
	}
	return 0
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (m *Model) stepCard(d int) {
	n := len(m.st.Game.Cards)
	if n == 0 {
		return
	}
	m.card = (m.card + d + n) % n
	m.cur = 0
}
