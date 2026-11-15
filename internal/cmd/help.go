package cmd

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"hb/internal/ui"
)

// The help screens: grouped, one line per command, headings and the names of
// commands in colour where the terminal has it (plain text in a pipe).

// usageTemplate is cobra's, in Chinese, with headings and command names styled.
const usageTemplate = `{{h "用法"}}{{if .Runnable}}
  {{useLine .}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} <命令> [选项]{{end}}{{if gt (len .Aliases) 0}}

{{h "别名"}}
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

{{h "示例"}}
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

{{h "命令"}}{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{name (rpad .Name .NamePadding)}} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{h .Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{name (rpad .Name .NamePadding)}} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

{{h "其他"}}{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{name (rpad .Name .NamePadding)}} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

{{h "选项"}}
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

{{h "全局选项"}}
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

{{dim (printf "用 \"%s <命令> --help\" 看某个命令的详细说明" .CommandPath)}}{{end}}
`

func init() {
	// Help goes to the terminal's stdout; its colours follow that.
	u := ui.For(os.Stdout)
	cobra.AddTemplateFunc("h", u.Section)
	cobra.AddTemplateFunc("name", u.Accent)
	cobra.AddTemplateFunc("dim", u.Dim)
	cobra.AddTemplateFunc("useLine", func(c *cobra.Command) string {
		return strings.ReplaceAll(c.UseLine(), "[flags]", "[选项]")
	})
}

// flagUsage is each option's description, by name: the same option means the
// same wherever it is used.
var flagUsage = map[string]string{
	"help":            "显示帮助",
	"version":         "显示版本",
	"json":            "输出 JSON（键名始终是英文，不含密钥）",
	"profile":         "账号档案名（默认用配置里的 default_profile）",
	"method":          "登录方式：ext | clone | paste | clean | token（默认询问）",
	"from-profile":    "clone：要复制的浏览器 profile 目录名",
	"keep-clone":      "clone：用完保留复制出的 profile（默认删除）",
	"timeout":         "等你完成登录的最长时间",
	"token-stdin":     "有个人令牌的站点（lafish、unsnow）：从标准输入读令牌，不再询问",
	"mode":            "玩法：auto | tui | cli | text | web",
	"input":           "文字模式：向红包的判定器提交这一条内容（一次）",
	"site":            "矿洞夺宝的游戏站地址（默认取自红包附带的网页）",
	"cli":             "安静的一行一个命令模式，不进全屏",
	"exec":            "执行这些命令（用 ; 分隔）后退出，比如 -x \"d 3 4\"",
	"auto-claim":      "机会用完或收尾开始时，自动领取已经挖到的（只领取，从不替你挖）",
	"hints":           "开启建议（默认关）：标出不可能有危险的格子，估算其余格子的风险；只提示，每次挖掘都由你来",
	"hints-log":       "开启建议，并在本地记下你每次挖之前它的判断和结果，之后用 hb hints report 检验准不准",
	"render":          "图标模式：auto | glyph | sprite（像素图标要真彩色终端，字体调小图标更大，按 z 切换）",
	"file":            "日志文件（默认是配置目录里的 hints-log.jsonl）",
	"no-descriptions": "补全时不带说明",
}

// shorts is each command's one line, by full path. Longer explanations are in
// the command's own Long.
var shorts = map[string]string{
	"hb assets":          "管理 Minecraft 原版图标（现场下载，不随 hb 分发）",
	"hb assets fetch":    "下载并核对图标（约 28 MB，一次性）",
	"hb assets check":    "核对已有的图标",
	"hb assets clean":    "删除已下载的图标",
	"hb hints":           "矿洞夺宝建议的校准报告",
	"hb hints report":    "看建议准不准（读 --hints-log 记下的日志）",
	"hb kddb":            "在终端里玩矿洞夺宝",
	"hb play":            "玩一个 lafish 红包（矿洞夺宝进终端游戏）",
	"hb login":           "登录某个站点并保存会话",
	"hb unsnow":          "Unsnow 红包（官方只读接口）",
	"hb unsnow check":    "查一个红包现在能不能领（不会领取）",
	"hb unsnow plaza":    "看公开红包广场",
	"hb unsnow me":       "令牌属于谁，口令查错次数和验证状态",
	"hb unsnow sent":     "我发出的红包",
	"hb unsnow received": "我已到账的红包",
	"hb help":            "查看某个命令的帮助",
	"hb completion":      "生成命令补全脚本（bash、zsh、fish、powershell）",
}

// groups are the sections of the top-level help, in order.
var groups = []struct{ id, title string }{
	{"account", "账号"},
	{"lafish", "lafish（hb.lafi.sh）"},
	{"unsnow", "Unsnow（hb.unsnow.org）"},
}

var groupOf = map[string]string{
	"login": "account", "logout": "account", "whoami": "account",
	"play": "lafish", "kddb": "lafish", "assets": "lafish", "hints": "lafish",
	"unsnow": "unsnow",
}

// polish gives the whole tree its help screens: the template, the groups, one
// line per command, and Chinese for the options and the commands cobra adds.
func polish(root *cobra.Command) {
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	root.InitDefaultVersionFlag()
	for _, g := range groups {
		root.AddGroup(&cobra.Group{ID: g.id, Title: g.title})
	}
	for _, c := range root.Commands() {
		c.GroupID = groupOf[c.Name()]
	}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		c.SetUsageTemplate(usageTemplate)
		if s, ok := shorts[c.CommandPath()]; ok {
			// A long Short is cut down; what it said stays in Long if there is none.
			if c.Long == "" && c.Short != s && len([]rune(c.Short)) > len([]rune(s)) {
				c.Long = c.Short
			}
			c.Short = s
		}
		c.InitDefaultHelpFlag()
		c.Flags().VisitAll(localize)
		c.PersistentFlags().VisitAll(localize)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		if c, _, err := root.Find([]string{"completion", shell}); err == nil && c.Name() == shell {
			c.Short = "生成 " + shell + " 的补全脚本"
		}
	}
}

func localize(f *pflag.Flag) {
	if u, ok := flagUsage[f.Name]; ok {
		f.Usage = u
	}
}
