package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"hb/internal/assets"
	"hb/internal/auth"
	"hb/internal/game/kddb"
	"hb/internal/provider"
	"hb/internal/provider/lafish"
	"hb/internal/store"
)

// knownKddbHost is where the official 矿洞夺宝 game site lives (kddb README).
const knownKddbHost = "hbweb-kddb.lafi.sh"

func newKddbCmd() *cobra.Command {
	var site, exec string
	var cli bool
	cmd := &cobra.Command{
		Use:   "kddb <红包链接|id>",
		Short: "在终端里玩矿洞夺宝（lafish 上的多人挖宝红包）",
		Long: `在终端里玩矿洞夺宝（github.com/LeUKi/kddb）。

认人用你已登录的 lafish 会话，不需要浏览器；每一次挖掘都由你按键触发，
挖掘间隔、领取范围、奖池都由游戏站和红包站照旧把关，hb 不会自动连续挖。
领取后凭证会交给红包站的判定器结算；如果红包要求人机验证，凭证会保存下来，
请到红包页的指令框里粘贴提交。`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKddb(cmd, args[0], site, cli || exec != "", exec)
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "game site origin (default: taken from the packet's attached page)")
	cmd.Flags().BoolVar(&cli, "cli", false, "quiet line mode instead of the full-screen game: one command per line, short output")
	cmd.Flags().StringVarP(&exec, "exec", "x", "", "run these commands (separated by ;) in line mode and exit, e.g. -x \"d 3 4\"")
	cmd.Flags().BoolVar(&kddbAutoClaim, "auto-claim", false,
		"claim what you already earned when your chances run out or the wrap-up starts (like the web page); never digs")
	cmd.Flags().BoolVar(&kddbHints, "hints", false,
		"advisor (off by default): marks cells that cannot hold a danger and estimates the odds of the rest from what the game shows you; it only advises, every dig is yours")
	cmd.Flags().BoolVar(&kddbHintsLog, "hints-log", false,
		"turns the advisor on and records, for each dig YOU make, its guess and what the cell was, to check its accuracy later: hb hints report")
	cmd.Flags().StringVar(&kddbRender, "render", "auto",
		"auto | glyph | sprite: pixel icons need a true-colour terminal; a smaller font gives bigger icons (key z cycles)")
	return cmd
}

// Set by flags on both `hb kddb` and `hb play`.
var (
	kddbAutoClaim bool
	kddbHints     bool
	kddbHintsLog  bool
	kddbRender    = "auto"
)

// hintLog is the calibration log when --hints-log asked for one.
func hintLog() kddb.HintLog {
	if !kddbHintsLog {
		return nil
	}
	home, err := store.Home()
	if err != nil {
		return nil
	}
	return &kddb.FileHintLog{Path: filepath.Join(home, "hints-log.jsonl")}
}

// kddbOptions gathers what the terminal game needs to know about this
// terminal and the player's setup: true colour (for pixel icons), and
// Minecraft's textures if they were fetched with `hb assets fetch`.
func kddbOptions(packet string) kddb.Options {
	o := kddb.Options{
		AutoClaim: kddbAutoClaim,
		Hints:     kddbHints || kddbHintsLog, // a calibration log needs the advisor
		HintLog:   hintLog(),
		Packet:    packet,
		Render:    kddbRender,
		TrueColor: lipgloss.ColorProfile() == termenv.TrueColor,
	}
	if dir, err := assets.Dir(); err == nil {
		if pack, err := assets.Load(dir, assets.AllFiles()); err == nil {
			o.Pack = pack
		}
	}
	return o
}

// lafishSession is a logged-in lafish connection for one profile.
type lafishSession struct {
	lf *lafish.Lafish
	s  auth.Session
}

// openLafish loads the saved lafish session (with a CSRF token, which
// protected mutations such as page-identity and claim need).
func openLafish(ctx context.Context) (lafishSession, error) {
	prof, err := profileName()
	if err != nil {
		return lafishSession{}, err
	}
	s, err := auth.Load(prof, "lafish")
	if err != nil {
		return lafishSession{}, err
	}
	p, err := provider.Get("lafish")
	if err != nil {
		return lafishSession{}, err
	}
	lf, ok := p.(*lafish.Lafish)
	if !ok {
		return lafishSession{}, errors.New("lafish provider unavailable")
	}
	if s.CSRF == "" && s.Token == "" { // a personal token has no CSRF token
		if tok, err := lf.FetchCSRF(ctx, s); err == nil {
			s.CSRF = tok
		}
	}
	return lafishSession{lf, s}, nil
}

func runKddb(cmd *cobra.Command, ref, siteFlag string, cli bool, exec string) error {
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
	if cli {
		return playKddbCLI(cmd, ctx, ls, act, id, siteFlag, exec)
	}
	return playKddb(cmd, ctx, ls, act, id, siteFlag)
}

// playKddb opens the full-screen terminal game for a packet whose page is the
// 矿洞夺宝 game.
func playKddb(cmd *cobra.Command, ctx context.Context, ls lafishSession, act lafish.Activity, id, siteFlag string) error {
	if !interactive() {
		return ErrNotInteractive
	}
	client, submit, err := kddbConnect(cmd, ls, act, id, siteFlag)
	if err != nil {
		return err
	}
	model := kddb.NewModel(ctx, client, submit, kddbOptions(id))
	_, err = tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	return err
}

// playKddbCLI is the quiet, line-by-line mode: no full screen, one action per
// command. exec, if not empty, is run (";" separates lines) and then it exits;
// otherwise it reads commands from stdin until "q".
func playKddbCLI(cmd *cobra.Command, ctx context.Context, ls lafishSession, act lafish.Activity, id, siteFlag, exec string) error {
	client, submit, err := kddbConnect(cmd, ls, act, id, siteFlag)
	if err != nil {
		return err
	}
	sh := kddb.NewShell(ctx, client, submit, cmd.InOrStdin(), cmd.OutOrStdout())
	sh.Hints = kddbHints || kddbHintsLog
	sh.HintLog, sh.Packet = hintLog(), id
	if exec != "" {
		return sh.RunLines(strings.Split(exec, ";"))
	}
	return sh.Run(interactive())
}

// kddbConnect signs in to the game site as the player (through lafish's page
// identity) and returns the game client and the voucher submitter.
func kddbConnect(cmd *cobra.Command, ls lafishSession, act lafish.Activity, id, siteFlag string) (*kddb.Client, kddb.SubmitFunc, error) {
	lf, s := ls.lf, ls.s
	origin, err := gameSite(act, siteFlag)
	if err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "红包「%s」 游戏站 %s\n", act.Title, origin)
	if act.Status != "active" {
		fmt.Fprintf(cmd.ErrOrStderr(), "注意：红包状态是 %q，可能已不能挖。\n", act.Status)
	}

	identity := func(ctx context.Context, nonce string) (string, error) {
		raw, err := lf.PageIdentity(ctx, s, id, nonce)
		if err != nil {
			return "", fmt.Errorf("红包站不肯签发身份令牌: %w", err)
		}
		var out struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(raw, &out) != nil || out.Token == "" {
			return "", errors.New("红包站的身份令牌回复读不懂")
		}
		return out.Token, nil
	}
	client := kddb.New(origin, id, identity)
	submit := func(ctx context.Context, voucher string) (string, error) {
		return submitVoucher(ctx, lf, s, id, voucher)
	}
	return client, submit, nil
}

// gameSite works out the game site origin from the packet's attached page.
func gameSite(act lafish.Activity, siteFlag string) (string, error) {
	if siteFlag != "" {
		u, err := url.Parse(siteFlag)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			return "", fmt.Errorf("bad --site %q", siteFlag)
		}
		return u.Scheme + "://" + u.Host, nil
	}
	page := act.PageURL()
	if page == "" {
		return "", errors.New("这个红包没有附带网页，不是矿洞夺宝。")
	}
	u, err := url.Parse(page)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("附带网页地址读不懂: %q", page)
	}
	if !strings.EqualFold(u.Hostname(), knownKddbHost) {
		return "", fmt.Errorf("这个红包的附带网页是 %s，不是官方的矿洞夺宝（%s）。"+
			"如果是别人自己部署的矿洞夺宝，用 --site %s://%s 指定", u.Host, knownKddbHost, u.Scheme, u.Host)
	}
	return u.Scheme + "://" + u.Host, nil
}

// submitVoucher saves the voucher first (it is the proof of the payout), then
// hands it to lafish's judge as the input, as the game's own page does with hb.submit.
func submitVoucher(ctx context.Context, lf *lafish.Lafish, s auth.Session, id, voucher string) (string, error) {
	saved := ""
	if home, err := store.Home(); err == nil {
		dir := filepath.Join(home, "vouchers")
		if os.MkdirAll(dir, 0o700) == nil {
			path := filepath.Join(dir, id+".txt")
			line := time.Now().Format(time.RFC3339) + " " + voucher + "\n"
			if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				_, _ = f.WriteString(line)
				f.Close()
				saved = path
			}
		}
	}
	raw, err := lf.ClaimCode(ctx, s, id, voucher, "")
	if err != nil {
		var ae *lafish.APIError
		if errors.As(err, &ae) && ae.Code == "HUMAN_CHECK_REQUIRED" {
			return "", fmt.Errorf("这个红包要求人机验证，hb 不替你过验证。凭证已保存到 %s，请到红包页的指令框里粘贴提交", saved)
		}
		if saved != "" {
			return "", fmt.Errorf("%w（凭证已保存到 %s）", err, saved)
		}
		return "", err
	}
	return describeJudgeReply(raw), nil
}

// describeJudgeReply turns lafish's answer to a submitted voucher into one line,
// e.g. `{"result":"judged","message":"领取 6.28","moved":{"added":"6.28","pending":"6.28"},...}`
// becomes "红包站：领取 6.28 · 新增 6.28 · 待结算 6.28". An answer it does not
// recognise is shown (shortened) as it is.
func describeJudgeReply(raw []byte) string {
	var r struct {
		Result  string `json:"result"`
		Message string `json:"message"`
		Moved   *struct {
			Added   string `json:"added"`
			Pending string `json:"pending"`
		} `json:"moved"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Result == "" {
		text := strings.TrimSpace(string(raw))
		if len([]rune(text)) > 300 {
			text = string([]rune(text)[:300]) + "…"
		}
		return "红包站回复：" + text
	}
	parts := []string{r.Message}
	if r.Message == "" {
		parts = []string{r.Result}
	}
	if r.Moved != nil {
		if r.Moved.Added != "" {
			parts = append(parts, "新增 "+r.Moved.Added)
		}
		if r.Moved.Pending != "" {
			parts = append(parts, "待结算 "+r.Moved.Pending)
		}
	}
	prefix := "红包站"
	if r.Result != "judged" { // something other than a normal judgement: say what it was
		prefix += "（" + r.Result + "）"
	}
	return prefix + "：" + strings.Join(parts, " · ")
}
