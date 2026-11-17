package zyese

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// The game screen: the server's field drawn to the terminal's size, the keys
// the person types forwarded one character at a time. Esc pauses, Ctrl+C
// (twice) gives the run up. Pasted text is refused: only typing counts.

// Game is what the screen needs from the outside.
type Game struct {
	Client *Client
	HTTP   *http.Client
	Mode   string
	Trace  io.Writer
}

type (
	connectedMsg struct{ c *Conn }
	liveMsg      struct {
		c *Conn
		m Msg
	}
	liveEndMsg struct {
		c   *Conn
		err error
	}
	reconnectMsg struct{}
	startedMsg   struct {
		run Run
		err error
	}
	resultMsg struct{ state map[string]any }
	quitMsg   struct{}
)

// Model is the game screen.
type Model struct {
	g      Game
	ctx    context.Context
	run    Run
	conn   *Conn
	inputs Inputs
	start  time.Time

	state      *State
	ready      bool
	everReady  bool
	paused     bool
	over       bool
	overReason string
	result     map[string]any
	resyncing  bool
	lostAt     time.Time
	notice     string
	quitArmed  bool
	err        error

	width, height int
}

// NewModel starts a run in mode when the program starts.
func NewModel(ctx context.Context, g Game) *Model {
	return &Model{g: g, ctx: ctx, width: 80, height: 24}
}

func (m *Model) Init() tea.Cmd {
	return func() tea.Msg {
		r, err := m.g.Client.StartRun(m.ctx, m.g.Mode)
		return startedMsg{r, err}
	}
}

func (m *Model) dial() tea.Cmd {
	run := m.run
	return func() tea.Msg {
		tok, err := m.g.Client.Token(m.ctx)
		if err != nil {
			return liveEndMsg{nil, err}
		}
		c, err := Dial(m.ctx, m.g.HTTP, m.g.Client.Base, run.LivePath, tok, run.RunToken, m.g.Trace)
		if err != nil {
			return liveEndMsg{nil, err}
		}
		return connectedMsg{c}
	}
}

func wait(c *Conn) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-c.Msgs
		if !ok {
			return liveEndMsg{c, c.Err()}
		}
		return liveMsg{c, msg}
	}
}

func (m *Model) since() int64 { return time.Since(m.start).Milliseconds() }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case startedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.run, m.start = msg.run, time.Now()
		m.notice = "连接中…"
		return m, m.dial()
	case connectedMsg:
		m.conn, m.lostAt = msg.c, time.Time{}
		return m, wait(msg.c)
	case liveMsg:
		if msg.c != m.conn {
			return m, nil
		}
		m.handle(msg.m)
		if m.over {
			m.conn.Close()
			return m, m.fetchResult()
		}
		return m, wait(msg.c)
	case liveEndMsg:
		if msg.c != nil && msg.c != m.conn {
			return m, nil
		}
		return m, m.lost(msg.err)
	case reconnectMsg:
		if m.over || m.err != nil {
			return m, nil
		}
		m.notice = "重新连接中…"
		return m, m.dial()
	case resultMsg:
		m.result = msg.state
	case quitMsg:
		return m, tea.Quit
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

// handle applies one server message.
func (m *Model) handle(msg Msg) {
	switch msg.Type {
	case "ready":
		m.ready, m.everReady = true, true
		if msg.State != nil {
			m.state = msg.State
			m.inputs.Ack(msg.State.Ack)
		}
		m.notice = ""
		// after a reconnect: what the server has not seen goes again, with its numbers
		if un := m.inputs.Unacked(); len(un) > 0 && m.conn != nil {
			_ = m.conn.Resend(m.ctx, un)
		}
	case "snapshot":
		if msg.State != nil {
			m.state = msg.State
			m.inputs.Ack(msg.State.Ack)
		}
	case "input_resync":
		if msg.State != nil {
			m.state = msg.State
			m.inputs.Ack(msg.State.Ack)
		}
		m.resyncing = true
		ins, ok := m.inputs.From(msg.ExpectedSeq)
		if !ok {
			_ = m.conn.ResyncUnavailable(m.ctx, msg.ExpectedSeq, m.inputs.Next())
		} else {
			_ = m.conn.Resend(m.ctx, ins)
		}
		m.resyncing = false
	case "input_resync_unavailable":
		m.notice = "有几个按键服务器没收到，也补不上了"
	case "pong":
	case "gameover", "cheat", "logout":
		m.over, m.ready = true, false
		m.overReason = msg.Type
		if msg.State != nil {
			m.state = msg.State
		}
	}
	if m.state != nil && m.state.Status != "" {
		m.paused = m.state.Status == "paused"
	}
}

// lost: the connection ended. The run can come back within about two
// minutes, with a fresh token and the same run.
func (m *Model) lost(err error) tea.Cmd {
	m.ready = false
	if m.over {
		return nil
	}
	code := CloseCode(err)
	switch {
	case code == CloseExpired:
		m.err = errors.New("这局已经过期，不能继续了")
		return nil
	case !m.everReady:
		// the first connection never worked: give the run up, as the API asks
		run := m.run
		reason := "socket_closed"
		m.err = fmt.Errorf("连不上对局：%v", errText(err))
		return func() tea.Msg {
			_ = m.g.Client.ConnectionFailed(m.ctx, run, reason)
			return nil
		}
	}
	if m.lostAt.IsZero() {
		m.lostAt = time.Now()
	}
	if time.Since(m.lostAt) > 2*time.Minute {
		m.err = errors.New("断线超过两分钟，这局没法恢复了")
		return nil
	}
	m.notice = fmt.Sprintf("连接断了（%s），稍后重连…", errText(err))
	if code == CloseAuth {
		m.notice = "令牌到期或权限变化，刷新后重连…"
	}
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return reconnectMsg{} })
}

func errText(err error) string {
	if err == nil {
		return "已关闭"
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Error()
	}
	s := err.Error()
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

func (m *Model) fetchResult() tea.Cmd {
	id := m.run.RunID
	return func() tea.Msg {
		// the result may take a moment to settle: a few looks, a second apart
		for i := 0; i < 5; i++ {
			st, err := m.g.Client.RunState(m.ctx, id)
			if err == nil {
				if r, ok := st["result"]; ok && r != nil {
					return resultMsg{st}
				}
			}
			select {
			case <-time.After(time.Second):
			case <-m.ctx.Done():
				return nil
			}
		}
		return nil
	}
}

func (m *Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.Type == tea.KeyCtrlC {
		if m.over || m.err != nil || m.run.RunID == "" {
			return m, tea.Quit
		}
		if !m.quitArmed {
			m.quitArmed = true
			m.notice = "再按一次 Ctrl+C 放弃这局（会算作中途退出）"
			return m, nil
		}
		run := m.run
		if m.conn != nil {
			m.conn.Close()
		}
		return m, func() tea.Msg {
			_ = m.g.Client.Abandon(m.ctx, run, "player_exit")
			return quitMsg{}
		}
	}
	m.quitArmed = false
	if m.over || m.err != nil {
		if k.Type == tea.KeyEsc || k.String() == "q" || k.Type == tea.KeyEnter {
			return m, tea.Quit
		}
		return m, nil
	}
	if k.Type == tea.KeyEsc {
		if m.conn == nil || !m.ready {
			return m, nil
		}
		if m.paused {
			_ = m.conn.Resume(m.ctx)
			m.notice = ""
		} else {
			_ = m.conn.Pause(m.ctx)
			m.notice = "暂停中，Esc 继续"
		}
		m.paused = !m.paused
		return m, nil
	}
	if k.Paste {
		m.notice = "不接受粘贴：只算你亲手敲的键"
		return m, nil
	}
	var keys []rune
	switch k.Type {
	case tea.KeyRunes:
		keys = k.Runes // an input method commits several at once: one key each
	case tea.KeySpace:
		keys = []rune{' '}
	default:
		return m, nil
	}
	if !m.ready || m.conn == nil {
		m.notice = "还没连上，这下没有发出去"
		return m, nil
	}
	if m.paused {
		m.notice = "暂停中，Esc 继续"
		return m, nil
	}
	for _, r := range keys {
		in := m.inputs.Add(string(r), m.since())
		if !m.resyncing {
			_ = m.conn.Key(m.ctx, in)
		}
	}
	return m, nil
}

// ---- drawing ----

var (
	dim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	bold    = lipgloss.NewStyle().Bold(true)
	typed   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	active  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("52"))
	normal  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	dying   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	warnSt  = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	errSt   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	planeSt = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
)

func num(f float64) string {
	if f == math.Trunc(f) {
		return fmt.Sprintf("%.0f", f)
	}
	return fmt.Sprintf("%.1f", f)
}

func (m *Model) header() string {
	modeName := map[string]string{"en": "英文", "zh": "中文", "ja": "日文"}[m.g.Mode]
	s := bold.Render("打飞机") + dim.Render(" · "+modeName)
	if st := m.state; st != nil {
		s += fmt.Sprintf("  分数 %s  波次 %s  连击 %s  击落 %s  命中 %s / 失误 %s  %ss",
			bold.Render(num(st.Score)), num(st.Wave), num(st.Streak), num(st.Kills), num(st.Hits), num(st.Misses), num(math.Floor(st.Elapsed)))
		if on(st.Shield) {
			s += warnSt.Render("  护盾")
		}
		if on(st.TimeStop) {
			s += warnSt.Render("  时停")
		}
		if on(st.ScoreBoostMs) {
			s += warnSt.Render("  加分")
		}
	}
	return s
}

// on: an optional power-up field that is present and not empty or zero.
func on(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x > 0
	case string:
		return x != ""
	case map[string]any:
		if a, ok := x["active"].(bool); ok {
			return a
		}
		return len(x) > 0
	}
	return true
}

func (m *Model) View() string {
	if m.err != nil {
		return m.header() + "\n\n" + errSt.Render(m.err.Error()) + "\n\n" + dim.Render("按 Enter 或 q 退出") + "\n"
	}
	if m.over {
		return m.overView()
	}
	h := max(6, m.height-3)
	w := max(20, m.width)
	field := m.field(w, h)
	foot := dim.Render("敲键盘打下飞机 · Esc 暂停 · Ctrl+C 两次放弃")
	if m.notice != "" {
		foot = warnSt.Render(m.notice) + "  " + foot
	}
	return m.header() + "\n" + field + "\n" + foot
}

// The field is drawn as plain text with colour only where there is a word:
// a cell carries a style number (0: none), and a row is written as runs of
// one style. A frame is then a few kilobytes, not one escape code per blank
// cell, which a terminal could not keep up with (keys then arrive in bursts).
const (
	stPlain = iota
	stNormal
	stTyped
	stActive
	stActiveTyped
	stDying
	stPlane
)

var styles = []lipgloss.Style{
	stPlain:       lipgloss.NewStyle(),
	stNormal:      normal,
	stTyped:       typed,
	stActive:      active,
	stActiveTyped: active.Foreground(lipgloss.Color("10")),
	stDying:       dying,
	stPlane:       planeSt,
}

type cell struct {
	s  string
	st uint8
	w  uint8 // 0: the second half of a wide character
}

var blank = cell{" ", stPlain, 1}

// field draws the planes on a w×h grid, keeping the field's 480:720 proportion
// only as far as the terminal allows (the words have to be readable).
func (m *Model) field(w, h int) string {
	if m.state == nil {
		msg := "开局中…"
		if m.run.RunID != "" {
			msg = "连接对局…"
		}
		return center(msg, w, h)
	}
	grid := make([][]cell, h)
	for i := range grid {
		grid[i] = make([]cell, w)
		for j := range grid[i] {
			grid[i][j] = blank
		}
	}
	enemies := append([]Enemy(nil), m.state.Enemies...)
	// the target last, so it is drawn on top
	sort.SliceStable(enemies, func(i, j int) bool {
		return !SameID(enemies[i].ID, m.state.ActiveTargetID) && SameID(enemies[j].ID, m.state.ActiveTargetID)
	})
	for _, e := range enemies {
		row := clampInt(int(e.Y*float64(h-1)+0.5), 0, h-1)
		done, left := e.Progress()
		if e.NameStealth {
			// a hidden name stays hidden
			done, left = strings.Repeat("?", len([]rune(done))), strings.Repeat("?", len([]rune(left)))
		}
		tw := runewidth.StringWidth(done+left) + 2
		col := clampInt(int(e.X*float64(w-1)+0.5)-tw/2, 0, max(0, w-tw))
		st, dst := uint8(stNormal), uint8(stTyped)
		switch {
		case e.Dying:
			st, dst = stDying, stDying
		case SameID(e.ID, m.state.ActiveTargetID):
			st, dst = stActive, stActiveTyped
		}
		put(grid[row], col, "✈", stPlane)
		c := col + 2
		for _, r := range done {
			c = put(grid[row], c, string(r), dst)
		}
		for _, r := range left {
			c = put(grid[row], c, string(r), st)
		}
	}
	var b strings.Builder
	var run strings.Builder
	for i, row := range grid {
		// no trailing blanks: the renderer clears the rest of the line
		end := len(row)
		for end > 0 && row[end-1] == blank {
			end--
		}
		cur := uint8(stPlain)
		flush := func() {
			if run.Len() == 0 {
				return
			}
			if cur == stPlain {
				b.WriteString(run.String())
			} else {
				b.WriteString(styles[cur].Render(run.String()))
			}
			run.Reset()
		}
		for _, c := range row[:end] {
			if c.w == 0 {
				continue
			}
			if c.st != cur {
				flush()
				cur = c.st
			}
			run.WriteString(c.s)
		}
		flush()
		if i < len(grid)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// put writes one character at col (a wide one takes two cells) and returns the next column.
func put(row []cell, col int, s string, st uint8) int {
	w := runewidth.StringWidth(s)
	if col < 0 || col+w > len(row) {
		return col + w
	}
	// do not cut a wide character in half
	if row[col].w == 0 && col > 0 {
		row[col-1] = blank
	}
	if w == 2 && col+2 < len(row) && row[col+1].w == 2 {
		row[col+2] = blank
	}
	row[col] = cell{s, st, uint8(w)}
	for k := 1; k < w; k++ {
		row[col+k] = cell{"", st, 0}
	}
	return col + w
}

func center(s string, w, h int) string {
	var b strings.Builder
	for i := 0; i < h; i++ {
		if i == h/2 {
			pad := max(0, (w-runewidth.StringWidth(s))/2)
			b.WriteString(strings.Repeat(" ", pad) + dim.Render(s))
		}
		if i < h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func clampInt(v, lo, hi int) int { return max(lo, min(v, hi)) }

func (m *Model) overView() string {
	var b strings.Builder
	b.WriteString(m.header() + "\n\n")
	switch m.overReason {
	case "cheat":
		b.WriteString(errSt.Render("服务器判定这局输入异常，结束了。") + "\n")
		if m.state != nil && len(m.state.Result) > 0 {
			var r struct {
				CheatReason  string `json:"cheatReason"`
				Warning      bool   `json:"warning"`
				WarningCount int    `json:"warningCount"`
				Banned       bool   `json:"banned"`
			}
			if json.Unmarshal(m.state.Result, &r) == nil {
				if r.CheatReason != "" {
					b.WriteString(dim.Render("原因："+r.CheatReason) + "\n")
				}
				if r.Banned {
					b.WriteString(errSt.Render("账号已被封禁。") + "\n")
				} else if r.Warning {
					b.WriteString(warnSt.Render(fmt.Sprintf("这是第 %d 次警告；再被判定可能会封号，先别继续玩，等问清楚。", r.WarningCount)) + "\n")
				}
			}
		}
	case "logout":
		b.WriteString(errSt.Render("服务器让这局下线了（封禁、应用停用或在别处开了新局）。") + "\n")
	default:
		b.WriteString(bold.Render("游戏结束") + "\n")
	}
	if st := m.state; st != nil {
		fmt.Fprintf(&b, "\n分数 %s  波次 %s  击落 %s  最长连击 %s  用时 %ss\n",
			bold.Render(num(st.Score)), num(st.Wave), num(st.Kills), num(st.LongestStreak), num(math.Floor(st.Elapsed)))
		if st.FinishReason != nil && *st.FinishReason != "" {
			b.WriteString(dim.Render("结束原因："+*st.FinishReason) + "\n")
		}
	}
	if m.result != nil {
		if r, ok := m.result["result"].(map[string]any); ok {
			b.WriteString("\n" + bold.Render("服务器结算") + "\n")
			keys := make([]string, 0, len(r))
			for k := range r {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v, _ := json.Marshal(r[k])
				fmt.Fprintf(&b, "  %s  %s\n", dim.Render(k), string(v))
			}
		}
	} else {
		b.WriteString(dim.Render("\n正在读服务器的结算…") + "\n")
	}
	b.WriteString("\n" + dim.Render("按 Enter 或 q 退出") + "\n")
	return b.String()
}
