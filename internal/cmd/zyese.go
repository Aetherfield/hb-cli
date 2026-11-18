package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"hb/internal/auth"
	"hb/internal/ui"
	"hb/internal/zyese"
)

const zyeseProvider = "zyese"

func newZyeseCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "zyese",
		Aliases: []string{"dfj"},
		Short:   "打飞机（zyese.xyz）打字游戏：通过 Luminet 的 OpenAPI 在终端里玩",
		Long: "打飞机是 zyese.xyz 的打字游戏，hb 通过 Luminet 提供的 OpenAPI 接入。\n" +
			"登录走 Luminet Auth（浏览器里授权，hb 不碰你的密码）；令牌存在系统凭据库里。\n" +
			"游戏在服务器上跑，hb 只把你敲的键一个个转发过去：不代打、不接受粘贴、不上报分数。\n" +
			"成绩进网站的共享排行榜，标记为第三方应用。",
	}
	login := &cobra.Command{
		Use:   "login",
		Short: "在浏览器里登录 Luminet Auth",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prof, err := profileName()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			t, err := zyese.Login(cmd.Context(), zyeseHTTP(), nil, out)
			if err != nil {
				return err
			}
			if err := saveZyese(prof, t); err != nil {
				return err
			}
			fmt.Fprintln(out, "登录成功，令牌已存到"+backendName()+"。")
			// check the token is good for the game too, and say what is missing if not
			cl := zyeseClient(prof)
			me, err := cl.Me(cmd.Context())
			if err != nil {
				fmt.Fprintln(out, "不过打飞机接口还用不了：", err)
				return nil
			}
			fmt.Fprintf(out, "打飞机玩家：%s\n", playerName(me.User))
			return nil
		},
	}
	logout := &cobra.Command{
		Use:   "logout",
		Short: "删掉本机保存的打飞机令牌",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			prof, err := profileName()
			if err != nil {
				return err
			}
			if err := auth.Delete(prof, zyeseProvider); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "已删除。Luminet 那边的授权可以在 "+zyese.LinkAccountURL[:strings.Index(zyese.LinkAccountURL, "/account/")]+"/account 里撤销。")
			return nil
		},
	}
	me := &cobra.Command{
		Use:   "me",
		Short: "我是谁、应用是否获准、能玩哪些模式",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := openZyese()
			if err != nil {
				return err
			}
			m, err := cl.Me(cmd.Context())
			if err != nil {
				return err
			}
			if flagJSON {
				return writeJSON(cmd.OutOrStdout(), m)
			}
			out := cmd.OutOrStdout()
			st := ui.For(out)
			fmt.Fprintln(out, st.Bold("玩家")+"  "+playerName(m.User))
			writeFields(out, st, m.User, "  ")
			if len(m.Client) > 0 {
				fmt.Fprintln(out, st.Bold("应用"))
				writeFields(out, st, m.Client, "  ")
			}
			if len(m.Capabilities.InputModes) > 0 {
				fmt.Fprintln(out, st.Bold("模式")+"  "+strings.Join(m.Capabilities.InputModes, "、"))
			}
			return nil
		},
	}
	stats := &cobra.Command{
		Use:   "stats",
		Short: "我的战绩",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := openZyese()
			if err != nil {
				return err
			}
			m, err := cl.Stats(cmd.Context())
			if err != nil {
				return err
			}
			if flagJSON {
				return writeJSON(cmd.OutOrStdout(), m)
			}
			writeFields(cmd.OutOrStdout(), ui.For(cmd.OutOrStdout()), m, "")
			return nil
		},
	}
	var rankMode string
	rank := &cobra.Command{
		Use:       "rank [daily|yesterday|all-time|event]",
		Short:     "排行榜（默认今天、英文；event 是当前活动的榜，只有 en、zh）",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"daily", "yesterday", "all-time", "event"},
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := "daily"
			if len(args) == 1 {
				scope = args[0]
			}
			cl, err := openZyese()
			if err != nil {
				return err
			}
			m, err := cl.Leaderboard(cmd.Context(), scope, rankMode)
			if err != nil {
				return err
			}
			if flagJSON {
				return writeJSON(cmd.OutOrStdout(), m)
			}
			writeBoard(cmd.OutOrStdout(), ui.For(cmd.OutOrStdout()), m)
			return nil
		},
	}
	rank.Flags().StringVar(&rankMode, "mode", "en", "en | zh | ja")
	var playMode, tracePath string
	play := &cobra.Command{
		Use:   "play",
		Short: "开一局（全屏）",
		Long: "开一局打飞机。敲键盘打下飞机；Esc 暂停，Ctrl+C 按两次放弃。\n" +
			"中文、日文模式用输入法：输入法上屏的每个字各算一个键。粘贴的文字不算，会被拒绝。\n" +
			"开新局会替代你在这个应用里没打完的那局。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !interactive() {
				return ErrNotInteractive
			}
			cl, err := openZyese()
			if err != nil {
				return err
			}
			if zyese.IsHell(playMode) {
				if err := checkHell(cmd, cl, playMode); err != nil {
					return err
				}
			}
			g := zyese.Game{Client: cl, HTTP: zyeseHTTP(), Mode: playMode}
			if tracePath != "" {
				f, err := os.OpenFile(tracePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					return err
				}
				defer f.Close()
				g.Trace = f
			}
			ctx, stop := context.WithCancel(cmd.Context())
			defer stop()
			_, err = tea.NewProgram(zyese.NewModel(ctx, g), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
			if errors.Is(err, tea.ErrProgramKilled) {
				return nil
			}
			return err
		},
	}
	play.Flags().StringVar(&playMode, "mode", "en", "en | zh | ja")
	play.Flags().StringVar(&tracePath, "trace", "", "把服务器发来的每条消息记到这个文件（排查用；不含令牌）")
	hell := &cobra.Command{
		Use:   "hell",
		Short: "地狱模式：解锁进度和今天还剩几次",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := openZyese()
			if err != nil {
				return err
			}
			at, err := cl.Hell(cmd.Context())
			if err != nil {
				return err
			}
			if flagJSON {
				return writeJSON(cmd.OutOrStdout(), at)
			}
			out := cmd.OutOrStdout()
			u := ui.For(out)
			for _, mode := range []string{"hell-en", "hell-zh"} {
				a, ok := at[mode]
				if !ok {
					continue
				}
				fmt.Fprintln(out, u.Bold(modeName(mode))+"  "+hellLine(u, a))
			}
			return nil
		},
	}
	event := &cobra.Command{
		Use:   "event",
		Short: "当前的打字活动",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := openZyese()
			if err != nil {
				return err
			}
			ev, err := cl.ActiveEvent(cmd.Context())
			if err != nil {
				return err
			}
			if flagJSON {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"event": ev})
			}
			out := cmd.OutOrStdout()
			if ev == nil {
				fmt.Fprintln(out, ui.For(out).Dim("现在没有活动"))
				return nil
			}
			fmt.Fprintf(out, "%s  %s – %s\n", ui.For(out).Bold(ev.Title), msTime(ev.StartsAt), msTime(ev.EndsAt))
			fmt.Fprintln(out, ui.For(out).Dim("活动榜：hb zyese rank event --mode en（或 zh）"))
			return nil
		},
	}
	c.AddCommand(login, logout, me, stats, rank, hell, event, play)
	return c
}

func modeName(mode string) string {
	if n, ok := map[string]string{"en": "英文", "zh": "中文", "ja": "日文", "hell-en": "地狱 · 英文", "hell-zh": "地狱 · 中文"}[mode]; ok {
		return n
	}
	return mode
}

func msTime(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return time.UnixMilli(ms).Local().Format("01-02 15:04")
}

// hellLine is one hell mode's state, in words.
func hellLine(u *ui.UI, a zyese.HellAttempts) string {
	if !a.Unlocked {
		return u.Warn("未解锁") + fmt.Sprintf("：在%s模式里一局击落 %d、打到第 %d 波（你最好的一局：击落 %d、第 %d 波）",
			modeName(a.NormalMode), a.RequiredKills, a.RequiredWaves, a.BestKills, a.CompletedWaves)
	}
	if a.Unlimited || a.Remaining < 0 {
		return u.Good("已解锁") + "，今天不限次数"
	}
	s := u.Good("已解锁") + fmt.Sprintf("，今天还剩 %d / %d 次", a.Remaining, a.Limit)
	if a.ResetAt > 0 {
		s += u.Dim("（" + msTime(a.ResetAt) + " 重置）")
	}
	return s
}

// checkHell looks before a hell run: a locked mode or no attempts left is
// said, and nothing is started; otherwise the person confirms using one.
func checkHell(cmd *cobra.Command, cl *zyese.Client, mode string) error {
	at, err := cl.Hell(cmd.Context())
	if err != nil {
		return err
	}
	a, ok := at[mode]
	if !ok {
		return nil // the server will say
	}
	u := ui.For(cmd.ErrOrStderr())
	switch {
	case !a.Unlocked:
		return fmt.Errorf("%s %s", modeName(mode), hellLine(u, a))
	case a.Unlimited || a.Remaining < 0:
		return nil
	case a.Remaining == 0:
		return fmt.Errorf("%s 今天的次数用完了（%d / %d），%s 重置", modeName(mode), a.Used, a.Limit, msTime(a.ResetAt))
	}
	v, err := selectOne(fmt.Sprintf("开一局%s会用掉今天的 1 次（还剩 %d / %d），开吗？", modeName(mode), a.Remaining, a.Limit),
		[]choice{{"开", "yes"}, {"算了", "no"}})
	if err != nil {
		return err
	}
	if v != "yes" {
		return errors.New("没开局")
	}
	return nil
}

func zyeseHTTP() *http.Client { return &http.Client{Timeout: 20 * time.Second} }

func backendName() string {
	if auth.LastBackend() == auth.BackendFile {
		return "本机文件（这台机器没有可用的系统凭据库）"
	}
	return "系统凭据库"
}

func saveZyese(prof string, t zyese.Tokens) error {
	return auth.Save(prof, auth.Session{Provider: zyeseProvider, Token: t.Access, Refresh: t.Refresh, TokenExpiry: t.Expiry})
}

// openZyese is the API client for the saved sign-in.
func openZyese() (*zyese.Client, error) {
	prof, err := profileName()
	if err != nil {
		return nil, err
	}
	if _, err := auth.Load(prof, zyeseProvider); err != nil {
		if errors.Is(err, auth.ErrNoSession) {
			return nil, errors.New("还没登录打飞机：先运行 hb zyese login")
		}
		return nil, err
	}
	return zyeseClient(prof), nil
}

func zyeseClient(prof string) *zyese.Client {
	return &zyese.Client{Base: zyese.APIBase, HTTP: zyeseHTTP(), Token: zyeseTokenFunc(prof)}
}

// zyeseTokenFunc gives the access token, refreshed first when it has less
// than a minute left. Refreshes are one at a time; a new refresh token
// replaces the old one at once.
func zyeseTokenFunc(prof string) zyese.TokenFunc {
	var mu sync.Mutex
	return func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		s, err := auth.Load(prof, zyeseProvider)
		if err != nil {
			return "", err
		}
		if s.Token != "" && time.Until(s.TokenExpiry) > time.Minute {
			return s.Token, nil
		}
		t, err := zyese.Refresh(ctx, zyeseHTTP(), s.Refresh)
		if err != nil {
			if errors.Is(err, zyese.ErrReauth) {
				_ = auth.Delete(prof, zyeseProvider) // a dead sign-in is not kept
			}
			return "", err
		}
		if err := saveZyese(prof, t); err != nil {
			return "", err
		}
		return t.Access, nil
	}
}

func playerName(u map[string]any) string {
	for _, k := range []string{"displayName", "playerName", "nickname", "name", "username"} {
		if v, ok := u[k].(string); ok && v != "" {
			return v
		}
	}
	return "（接口没给名字）"
}

// writeFields prints an object's fields as they come, one per line, nested
// ones indented.
func writeFields(out io.Writer, st *ui.UI, m map[string]any, indent string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := m[k].(type) {
		case map[string]any:
			fmt.Fprintln(out, indent+st.Bold(k))
			writeFields(out, st, v, indent+"  ")
		case []any:
			b, _ := json.Marshal(v)
			s := string(b)
			if len(s) > 200 {
				s = fmt.Sprintf("（%d 项，--json 看全部）", len(v))
			}
			fmt.Fprintf(out, "%s%s  %s\n", indent, st.Dim(k), s)
		default:
			b, _ := json.Marshal(v)
			fmt.Fprintf(out, "%s%s  %s\n", indent, st.Dim(k), string(b))
		}
	}
}

// writeBoard prints a leaderboard: rank, name, score, wave, kills, accuracy,
// and where the run was played when it was not the web page.
func writeBoard(out io.Writer, st *ui.UI, m map[string]any) {
	entries, _ := m["entries"].([]any)
	if len(entries) == 0 {
		fmt.Fprintln(out, st.Dim("榜上还没有人"))
		return
	}
	numOf := func(row map[string]any, k string) string {
		if v, ok := row[k].(float64); ok {
			return fmt.Sprintf("%.0f", v)
		}
		return "-"
	}
	right := func(s string, w int) string { return strings.Repeat(" ", max(0, w-ui.Width(s))) + s }
	fmt.Fprintln(out, st.Dim(right("名次", 4)+"  "+ui.Pad("玩家", 20)+" "+right("分数", 8)+" "+right("波次", 4)+" "+right("击落", 4)+" "+right("准确率", 6)))
	for i, e := range entries {
		row, _ := e.(map[string]any)
		rank := fmt.Sprint(i + 1)
		if r, ok := row["rank"].(float64); ok {
			rank = fmt.Sprintf("%.0f", r)
		}
		acc := numOf(row, "accuracy")
		if acc != "-" {
			acc += "%"
		}
		where := ""
		if p, _ := row["platform"].(string); p != "" && p != "web" {
			where = st.Dim("  " + p)
		}
		if app, _ := row["clientName"].(string); app != "" {
			where = st.Dim("  第三方应用 · " + app)
		}
		fmt.Fprintf(out, "%4s  %s %8s %4s %4s %6s%s\n", rank, ui.Pad(playerName(row), 20), numOf(row, "score"), numOf(row, "wave"), numOf(row, "kills"), acc, where)
	}
}
