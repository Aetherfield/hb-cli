package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hb/internal/ecoji"
	"hb/internal/httpx"
	"hb/internal/provider/lafish"
)

// Play modes.
const (
	modeAuto = "auto"
	modeTUI  = "tui"
	modeText = "text"
	modeCLI  = "cli"
	modeWeb  = "web"
)

func newPlayCmd() *cobra.Command {
	var mode, site, input, exec string
	cmd := &cobra.Command{
		Use:   "play <红包链接|id>",
		Short: "玩一个 lafish 红包：认出矿洞夺宝就进终端游戏，否则看文字信息或提交一次",
		Long: `玩一个 lafish 红包。默认（--mode auto）按红包的附带网页识别：

  官方的矿洞夺宝   进终端游戏（等价于 hb kddb）
  没有附带网页     文字模式：显示红包和判定器的公开信息，用 --input 提交一次
  其他附带网页     网页宿主模式还没实现，先按文字模式处理

想低调一点玩矿洞夺宝（不进全屏）：--mode cli。一行一个命令，输出一两行，
d 3 4 挖第 3 行第 4 列，s 看状态，m 看文本地图，b 老板键清屏；
-x "d 3 4" 只执行一条就退出（可以绑别名或快捷键）。

每一次挖掘或提交都由你触发；hb 不会替你循环尝试，也不会替你过人机验证。`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPlay(cmd, args[0], mode, site, input, exec)
		},
	}
	f := cmd.Flags()
	f.StringVar(&mode, "mode", modeAuto, "auto | tui | cli | text | web")
	f.StringVarP(&exec, "exec", "x", "", "cli mode: run these commands (separated by ;) and exit, e.g. -x \"d 3 4\"")
	f.StringVar(&site, "site", "", "game site origin of a self-hosted 矿洞夺宝 (forces the terminal game)")
	f.StringVar(&input, "input", "", "text mode: submit this input once to the packet's judge")
	f.BoolVar(&kddbAutoClaim, "auto-claim", false, "terminal game: claim what you already earned when chances run out or the wrap-up starts; never digs")
	f.StringVar(&kddbRender, "render", "auto", "terminal game: auto | glyph | sprite")
	f.BoolVar(&kddbHints, "hints", false, "terminal game: advisor (off by default); marks cells that cannot hold a danger, never digs for you")
	f.BoolVar(&kddbHintsLog, "hints-log", false, "terminal game: advisor plus a local log of its guesses for the digs you make (hb hints report)")
	return cmd
}

const (
	kindKddb = "kddb" // the official 矿洞夺宝 game site
	kindNone = "none" // no attached page
	kindWeb  = "web"  // some other attached page
)

// detectGame classifies a packet by its attached page. An explicit --site
// means the player says it is a self-hosted 矿洞夺宝.
func detectGame(act lafish.Activity, siteFlag string) string {
	page := act.PageURL()
	if page == "" {
		return kindNone
	}
	if siteFlag != "" {
		return kindKddb
	}
	if u, err := url.Parse(page); err == nil && strings.EqualFold(u.Hostname(), knownKddbHost) {
		return kindKddb
	}
	return kindWeb
}

// resolvePacketRef turns what a person pastes into the packet's UUID: lafish
// shows people https://hb.lafi.sh/🧧/<14 emoji>, never the UUID.
func resolvePacketRef(arg string) (string, error) {
	id, ok := ecoji.ParseRef(arg)
	if !ok {
		return "", fmt.Errorf("认不出这个红包：%q。可以粘贴红包链接（https://hb.lafi.sh/🧧/…）、那串 14 个 emoji，或红包的 UUID", arg)
	}
	return id, nil
}

func runPlay(cmd *cobra.Command, ref, mode, siteFlag, input, exec string) error {
	id, err := resolvePacketRef(ref)
	if err != nil {
		return err
	}
	ctx, stop := context.WithCancel(cmd.Context())
	defer stop()
	ls, err := openLafish(ctx)
	if err != nil {
		return err
	}
	act, err := ls.lf.GetActivity(ctx, ls.s, id)
	if err != nil {
		return fmt.Errorf("read packet %s: %w", id, err)
	}
	kind := detectGame(act, siteFlag)

	switch mode {
	case "", modeAuto:
		switch {
		case input != "":
			mode = modeText // an explicit one-off submit
		case exec != "" && kind == kindKddb:
			mode = modeCLI // -x means the quiet line mode
		case kind == kindKddb:
			mode = modeTUI
		default:
			mode = modeText
			if kind == kindWeb {
				fmt.Fprintf(cmd.ErrOrStderr(), "这个红包的附带网页是 %s，不是已知的游戏；网页宿主模式还没实现，先按文字模式处理。\n", act.PageURL())
			}
		}
	case modeTUI, modeCLI, modeText, modeWeb:
	default:
		return fmt.Errorf("unknown --mode %q (auto|tui|cli|text|web)", mode)
	}

	switch mode {
	case modeCLI:
		if kind != kindKddb {
			return errors.New("行模式只有矿洞夺宝有；这个红包用 --mode text，或用 --site 指定自己部署的游戏站")
		}
		return playKddbCLI(cmd, ctx, ls, act, id, siteFlag, exec)
	case modeTUI:
		if kind != kindKddb {
			return errors.New("这个红包不是矿洞夺宝，没有对应的终端游戏；用 --mode text，或用 --site 指定自己部署的游戏站")
		}
		return playKddb(cmd, ctx, ls, act, id, siteFlag)
	case modeWeb:
		return errors.New("网页宿主模式（--mode web）还没实现")
	default:
		return playText(cmd, ctx, ls, act, id, input)
	}
}

// playText prints what the packet shows publicly and, with input, submits it once.
func playText(cmd *cobra.Command, ctx context.Context, ls lafishSession, act lafish.Activity, id, input string) error {
	out := cmd.OutOrStdout()
	if flagJSON && input == "" {
		_, err := fmt.Fprintln(out, string(act.Raw))
		return err
	}
	fmt.Fprintf(out, "%s  [%s]\n", act.Title, act.Status)
	if act.DeadlineAt != "" {
		fmt.Fprintf(out, "截止：%s\n", act.DeadlineAt)
	}
	if act.HumanCheck {
		fmt.Fprintf(out, "领取前要人机验证（%s）：hb 不替你过，提交时会提示去红包页。\n", act.HumanCheckMethod)
	}
	if j := act.Judge; j != nil {
		if p := str(j, "prompt"); p != "" {
			fmt.Fprintf(out, "提示：%s\n", p)
		}
		fmt.Fprintf(out, "判定器：已判定 %v 次", num(j["judged"]))
		if u := str(j, "unclaimed"); u != "" {
			fmt.Fprintf(out, " · 奖池还剩 %s", u)
		}
		fmt.Fprintln(out)
		if fp := str(j, "fingerprint"); fp != "" {
			fmt.Fprintf(out, "代码指纹：%s\n", fp)
		}
		if _, paused := j["paused"]; paused {
			fmt.Fprintln(out, "注意：判定器连续出错，领取已暂停。")
		}
		if _, stopped := j["stopped"]; stopped {
			fmt.Fprintln(out, "注意：站长已停用这个判定器。")
		}
		if v, ok := j["view"].(map[string]any); ok {
			if sum := str(v, "summary"); sum != "" {
				fmt.Fprintf(out, "公开数据：%s\n", sum)
			}
		}
	}
	if page := act.PageURL(); page != "" {
		fmt.Fprintf(out, "附带网页：%s\n", page)
	}

	if input == "" {
		return nil
	}
	raw, err := ls.lf.ClaimCode(ctx, ls.s, id, input, "")
	if err != nil {
		var rl *httpx.RateLimitedError
		var ae *lafish.APIError
		switch {
		case errors.As(err, &rl):
			return fmt.Errorf("请求太频繁，或者还在冷却：%s 后再试", rl.RetryAfter.Round(time.Second))
		case errors.As(err, &ae) && ae.Code == "HUMAN_CHECK_REQUIRED":
			return errors.New("这个红包要人机验证，hb 不替你过：请到红包页的指令框里提交")
		}
		return err
	}
	fmt.Fprintln(out, "\n提交结果：")
	var pretty any
	if json.Unmarshal(raw, &pretty) == nil {
		b, _ := json.MarshalIndent(pretty, "", "  ")
		fmt.Fprintln(out, string(b))
	} else {
		fmt.Fprintln(out, string(raw))
	}
	return nil
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

func num(v any) any {
	if v == nil {
		return 0
	}
	return v
}
