package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"hb/internal/auth"
	"hb/internal/game/kddb"
	"hb/internal/mcpserver"
	"hb/internal/provider"
	"hb/internal/provider/lafish"
	"hb/internal/provider/unsnow"
	"hb/internal/store"
)

// kddbGateway opens a packet's 矿洞夺宝 for the MCP server, with the same game
// client the terminal game uses. Only the official game site: a packet whose
// page is somewhere else is not played through an assistant.
type kddbGateway struct{ lf *lafish.Lafish }

func (g kddbGateway) Open(ctx context.Context, ses auth.Session, id string) (mcpserver.KddbGame, error) {
	act, err := g.lf.GetActivity(ctx, ses, id)
	if err != nil {
		return nil, err
	}
	if act.Status != "active" {
		return nil, fmt.Errorf("这个红包现在不是进行中（%s）", act.Status)
	}
	client, submit, _, err := kddbClient(g.lf, ses, act, id, "")
	if err != nil {
		return nil, err
	}
	return &mcpGame{client: client, submit: submit, title: act.Title}, nil
}

type mcpGame struct {
	client *kddb.Client
	submit kddb.SubmitFunc
	title  string
}

func (m *mcpGame) Title() string                                 { return m.title }
func (m *mcpGame) State(ctx context.Context) (kddb.State, error) { return m.client.State(ctx) }
func (m *mcpGame) Dig(ctx context.Context, card, index int) (kddb.DigAnswer, error) {
	return m.client.Dig(ctx, card, index)
}
func (m *mcpGame) Claim(ctx context.Context) (kddb.ClaimAnswer, error) { return m.client.Claim(ctx) }
func (m *mcpGame) Submit(ctx context.Context, voucher string) (string, error) {
	return m.submit(ctx, voucher)
}

func newMCPCmd() *cobra.Command {
	lim := mcpserver.DefaultLimits()
	var readOnly bool
	c := &cobra.Command{
		Use:   "mcp",
		Short: "启动 MCP 服务，让 AI 助手查红包、在你确认后领红包",
		Long: "用 stdio 启动 hb 的 MCP 服务，给 Claude Code 这类 AI 助手用：\n\n" +
			"  claude mcp add hb -- hb mcp\n\n" +
			"能做：看登录的账号、查 lafish 和 Unsnow 的红包、看 Unsnow 的广场和记录；用户明确要求时，\n" +
			"领一个「点一下就领」的 lafish 红包，或者在用户授权的范围里玩矿洞夺宝。\n\n" +
			"限制写在代码里，AI 改不了：\n" +
			"  · 只用个人令牌领（名单上有标记，发红包的人能关）；用 cookie 登录时不领\n" +
			"  · 先预览、再领取；每一次领取都要你在客户端里点确认，客户端不能弹确认就不领，没有免确认的开关\n" +
			"  · 不处理口令、人机验证和代码判定红包；不能查 Unsnow 的口令\n" +
			"  · 矿洞夺宝：用户在客户端里授权一批（最多 10 次、10 分钟、一次一个授权），挖到危险的东西或出任何差错授权立刻作废；\n" +
			"    领取奖励要单独确认，领取后不能再挖\n" +
			"  · 两次领取至少隔 10 秒，默认每小时 5 个、每天 20 个；失败后暂停，连续失败熔断；失败不重试\n" +
			"  · 所有调用记在配置目录的 mcp-audit.jsonl（不含令牌）\n\n" +
			"配套的 Skill 在仓库的 skills/hb-red-packet，复制到 ~/.claude/skills/ 就行。",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if lim.PerHour < 1 || lim.PerHour > mcpserver.CeilingPerHour || lim.PerDay < 1 || lim.PerDay > mcpserver.CeilingPerDay {
				return fmt.Errorf("--max-per-hour 要在 1–%d，--max-per-day 要在 1–%d（这是上限，不能更高）", mcpserver.CeilingPerHour, mcpserver.CeilingPerDay)
			}
			prof, err := profileName()
			if err != nil {
				return err
			}
			home, err := store.Home()
			if err != nil {
				return err
			}
			lp, err := provider.Get("lafish")
			if err != nil {
				return err
			}
			up, err := provider.Get("unsnow")
			if err != nil {
				return err
			}
			lf, ok1 := lp.(*lafish.Lafish)
			un, ok2 := up.(*unsnow.Unsnow)
			if !ok1 || !ok2 {
				return errors.New("providers unavailable")
			}
			srv := mcpserver.New(mcpserver.Options{
				Lafish: lf, Unsnow: un, Kddb: kddbGateway{lf}, ReadOnly: readOnly, Version: version(),
				Session:    func(site string) (auth.Session, error) { return auth.Load(prof, site) },
				Guard:      mcpserver.NewGuard(filepath.Join(home, "mcp-guard.json"), lim, time.Now),
				GrantGuard: mcpserver.NewGuard(filepath.Join(home, "mcp-grants.json"), mcpserver.DefaultGrantLimits(), time.Now),
				Audit:      mcpserver.NewAudit(filepath.Join(home, "mcp-audit.jsonl"), time.Now),
			})
			// stdout is the protocol: nothing else may be written to it
			fmt.Fprintln(os.Stderr, "hb mcp: 已启动（stdio）。只读模式:", readOnly)
			return srv.Run(cmd.Context(), &mcp.StdioTransport{})
		},
	}
	f := c.Flags()
	f.BoolVar(&readOnly, "read-only", false, "只读：不提供领取")
	f.IntVar(&lim.PerHour, "max-per-hour", lim.PerHour, fmt.Sprintf("每小时最多领几个（上限 %d）", mcpserver.CeilingPerHour))
	f.IntVar(&lim.PerDay, "max-per-day", lim.PerDay, fmt.Sprintf("每 24 小时最多领几个（上限 %d）", mcpserver.CeilingPerDay))
	return c
}
