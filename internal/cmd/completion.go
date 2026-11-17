package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hb/internal/browser"
	"hb/internal/provider"
)

// What Tab offers. cobra answers `hb __complete <words> <current>`; the shells'
// scripts ask it. Values are offered where there is a short list to pick from
// (sites, login methods, modes). Never a passcode, a link or a packet id: a
// completion must not help to guess one.

// niuScript is the compdef-protocol bridge for niubash, whose Tab does not run
// `complete -F` functions yet (docs/src/external-completion-providers.md in
// unixwin/niubash): NIU_COMP_WORDS and NIU_COMP_CWORD in, NIU_COMP_RESULT out,
// one `value<TAB>description` per line, which is what `hb __complete` prints.
const niuScript = `# hb 在 niubash 里的补全（compdef 协议），用法见 hb completion niu --help。
_hb_niu_comp() {
    local -a words args
    local cur out line i
    words=($NIU_COMP_WORDS)
    cur=
    [ "$NIU_COMP_CWORD" -lt "${#words[@]}" ] && cur=${words[$NIU_COMP_CWORD]}
    args=()
    i=1
    while [ "$i" -lt "$NIU_COMP_CWORD" ]; do
        args+=("${words[$i]}")
        i=$((i + 1))
    done
    out=$(hb __complete "${args[@]}" "$cur" 2>/dev/null) || return 0
    NIU_COMP_RESULT=
    while IFS= read -r line; do
        case "$line" in
            "" | :[0-9]*) ;;
            *) NIU_COMP_RESULT="$NIU_COMP_RESULT$line"$'\n' ;;
        esac
    done <<< "$out"
}
__hb_nl=$'\n'
NIU_COMPDEFS="${NIU_COMPDEFS:+$NIU_COMPDEFS$__hb_nl}hb:_hb_niu_comp"
unset __hb_nl
`

// niuRC is what goes in ~/.niubashrc.
const niuRC = `  if command -v hb >/dev/null 2>&1; then
    __hb_c=$(hb completion niu 2>/dev/null)
    case "$__hb_c" in "# hb "*) eval "$__hb_c" ;; esac
    unset __hb_c
  fi
`

func newCompletionNiuCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "niu",
		Short: "生成 niubash 的补全脚本（compdef 协议）",
		Long: "输出 niubash 的补全脚本。niubash 的 Tab 目前不执行 `complete -F` 函数，要走它自己的 compdef 协议，\n" +
			"所以不能用 bash 的补全脚本。把下面这段加到 ~/.niubashrc（niubash 读不了 source <(...)，所以用 eval；\n" +
			"hb 不在 PATH 里或版本太旧时，这段会静默跳过）：\n\n" + niuRC + "\n" +
			"之后新开的 niubash 里，hb 的命令、选项、站点名和选项的取值都能用 Tab 补全。\n" +
			"口令、链接和红包编号不会被补全：补全不该帮人猜口令。",
		Args: cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprint(cmd.OutOrStdout(), niuScript)
		},
	}
}

// described is a value with its description, as cobra wants it.
func described(pairs ...string) []cobra.Completion {
	out := make([]cobra.Completion, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, cobra.CompletionWithDesc(pairs[i], pairs[i+1]))
	}
	return out
}

func siteNames(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []cobra.Completion
	for _, n := range provider.Names() {
		out = append(out, n)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func choices(pairs ...string) func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return described(pairs...), cobra.ShellCompDirectiveNoFileComp
	}
}

// addCompletions says what each command and option completes to.
func addCompletions(root *cobra.Command) {
	find := func(path ...string) *cobra.Command {
		c, _, err := root.Find(path)
		if err != nil || c == root {
			panic("completion: no command " + strings.Join(path, " "))
		}
		return c
	}
	for _, name := range []string{"login", "logout", "whoami"} {
		find(name).ValidArgsFunction = siteNames
	}
	// a link, a packet id or a passcode is the person's to type
	for _, path := range [][]string{{"play"}, {"kddb"}, {"unsnow", "check"}} {
		find(path...).ValidArgsFunction = cobra.NoFileCompletions
	}
	reg := func(c *cobra.Command, flag string, fn func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective)) {
		if err := c.RegisterFlagCompletionFunc(flag, fn); err != nil {
			panic(err)
		}
	}
	reg(find("login"), "method", choices(
		"token", "个人令牌",
		"ext", "浏览器扩展：用日常浏览器里已有的登录",
		"paste", "手动粘贴 cookie",
		"clean", "独立的干净浏览器",
		"clone", "已弃用：复制日常浏览器的 profile（新版 Chrome/Edge 读不到）",
	))
	reg(find("login"), "browser", func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		var out []cobra.Completion
		for _, id := range strings.Split(browser.KindIDs(), "|") {
			out = append(out, id)
		}
		return out, cobra.ShellCompDirectiveDefault // or a path to an exe
	})
	for _, name := range []string{"play", "kddb"} {
		reg(find(name), "render", choices("auto", "按终端自动选", "glyph", "字符图标", "sprite", "像素图标（要真彩色终端）"))
	}
	reg(find("play"), "mode", choices(
		"auto", "按红包的附带网页识别", "tui", "全屏游戏", "cli", "安静的一行一个命令", "text", "文字模式", "web", "网页宿主（还没实现）",
	))
}
