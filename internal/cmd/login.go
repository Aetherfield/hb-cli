package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hb/internal/auth"
	"hb/internal/browser"
	"hb/internal/ext"
	"hb/internal/i18n"
	"hb/internal/provider"
	_ "hb/internal/provider/lafish" // registers the lafish provider
	_ "hb/internal/provider/unsnow" // registers the unsnow provider
	"hb/internal/store"
	"hb/internal/ui"
)

func profileName() (string, error) {
	if flagProfile != "" {
		return flagProfile, nil
	}
	c, err := store.Load()
	if err != nil {
		return "", err
	}
	return c.DefaultProfile, nil
}

// Login methods.
const (
	methodExt   = "ext"   // MV3 extension in your everyday browser
	methodClone = "clone" // copy everyday profile, open it with a debug port
	methodPaste = "paste" // paste the cookie by hand
	methodClean = "clean" // dedicated empty profile, log in once
	methodToken = "token" // the site's own personal token, pasted
)

func newLoginCmd() *cobra.Command {
	var o loginOpts
	cmd := &cobra.Command{
		Use:   "login [站点]",
		Short: i18n.T("login.short"),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return runLogin(cmd, name, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.method, "method", "", "ext | clone | paste | clean | token (default: ask)")
	f.StringVar(&o.browserSpec, "browser", "",
		"浏览器："+browser.KindIDs()+"，或 exe 路径（默认用系统默认浏览器，或 $HB_BROWSER）")
	f.StringVar(&o.fromProfile, "from-profile", "Default", "clone: browser profile directory name to copy")
	f.BoolVar(&o.keepClone, "keep-clone", false, "clone: keep the copied profile afterwards (default: delete it)")
	f.DurationVar(&o.timeout, "timeout", 5*time.Minute, "how long to wait for you to finish logging in")
	f.BoolVar(&o.tokenStdin, "token-stdin", false, "sites with a personal token (lafish, unsnow): read it from stdin instead of asking")
	return cmd
}

type loginOpts struct {
	method      string
	browserSpec string
	fromProfile string
	keepClone   bool
	timeout     time.Duration
	tokenStdin  bool
}

func runLogin(cmd *cobra.Command, name string, o loginOpts) error {
	if name == "" {
		var opts []choice
		for _, n := range provider.Names() {
			opts = append(opts, choice{n, n})
		}
		var err error
		if name, err = selectOne(i18n.T("login.pick.provider"), opts); err != nil {
			return err
		}
	}
	p, err := provider.Get(name)
	if err != nil {
		return err
	}
	prof, err := profileName()
	if err != nil {
		return err
	}
	spec := p.LoginSpec()
	if (o.tokenStdin || o.method == methodToken) && spec.Token == nil {
		return fmt.Errorf("%s 没有个人令牌，不能用 --method token / --token-stdin", p.Name())
	}
	// A site with no cookie way in (Unsnow) is token only; one with both
	// (lafish) takes --method token, --token-stdin, or the choice below.
	if spec.Token != nil && (len(spec.CookieURLs) == 0 || o.tokenStdin) {
		o.method = methodToken
	}
	if o.method == "" {
		methods := []choice{
			{i18n.T("login.m.ext"), methodExt},
			{i18n.T("login.m.clone"), methodClone},
			{i18n.T("login.m.paste"), methodPaste},
			{i18n.T("login.m.clean"), methodClean},
		}
		if spec.Token != nil {
			methods = append(methods, choice{i18n.T("login.m.token"), methodToken})
		}
		if o.method, err = selectOne(i18n.T("login.pick.method"), methods); err != nil {
			if errors.Is(err, ErrNotInteractive) {
				return errors.New("pass --method ext|clone|paste|clean|token (no interactive terminal)")
			}
			return err
		}
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), o.timeout)
	defer cancel()

	var s auth.Session
	switch o.method {
	case methodToken:
		s, err = loginToken(cmd, p, *spec.Token, o.tokenStdin)
	case methodExt:
		s, err = loginExt(ctx, cmd, p)
	case methodClone:
		s, err = loginClone(ctx, cmd, p, o)
	case methodPaste:
		s, err = loginPaste(ctx, cmd, p)
	case methodClean:
		s, err = loginClean(ctx, cmd, p, o)
	default:
		return fmt.Errorf("unknown --method %q (ext|clone|paste|clean|token)", o.method)
	}
	if err != nil {
		return err
	}
	return finishLogin(ctx, cmd, p, prof, s)
}

// finishLogin fills a missing CSRF token, checks the session works, and saves it.
func finishLogin(ctx context.Context, cmd *cobra.Command, p provider.Provider, prof string, s auth.Session) error {
	if s.CSRF == "" && s.Token == "" { // a personal token has no CSRF token
		if f, ok := p.(provider.CSRFFetcher); ok {
			if tok, err := f.FetchCSRF(ctx, s); err == nil {
				s.CSRF = tok
			}
		}
	}
	id, err := p.Whoami(ctx, s)
	if err != nil {
		return fmt.Errorf("the captured session does not work: %w", err)
	}
	if err := auth.Save(prof, s); err != nil {
		return err
	}
	if auth.LastBackend() == auth.BackendFile {
		path, _ := auth.FallbackPath()
		fmt.Fprintln(cmd.ErrOrStderr(), i18n.T("login.file.warn", path))
	}
	u := ui.For(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), u.Mark(true)+" "+i18n.T("login.saved", p.Name(), prof)+" — "+u.Accent(id.Username))
	return nil
}

// loginExt: pair with the MV3 extension running in the everyday browser.
func loginExt(ctx context.Context, cmd *cobra.Command, p provider.Provider) (auth.Session, error) {
	home, err := store.Home()
	if err != nil {
		return auth.Session{}, err
	}
	dir, err := ext.Extract(home)
	if err != nil {
		return auth.Session{}, err
	}
	ln, err := ext.Listen()
	if err != nil {
		return auth.Session{}, err
	}
	pr, err := ext.NewPairing(p, ln.Addr().String())
	if err != nil {
		ln.Close()
		return auth.Session{}, err
	}
	srv := &http.Server{Handler: pr.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	w := cmd.ErrOrStderr()
	fmt.Fprintln(w, i18n.T("login.ext.install", dir))
	fmt.Fprintln(w, i18n.T("login.ext.code", p.Name(), pr.Code()))
	return pr.Wait(ctx)
}

// loginClone: take the login from the everyday browser's profile. Chromium
// family: copy the login files and open that copy with a debugging port.
// Firefox family: read the plain cookies.sqlite directly (browser may stay open).
func loginClone(ctx context.Context, cmd *cobra.Command, p provider.Provider, o loginOpts) (auth.Session, error) {
	home, err := store.Home()
	if err != nil {
		return auth.Session{}, err
	}
	b, err := chooseBrowser(o, false)
	if err != nil {
		return auth.Session{}, err
	}
	if b.Kind.Family == browser.Gecko {
		return loginGecko(cmd, p, b)
	}
	exe := b.Exe
	ud, err := browser.UserDataDir(b.Kind)
	if err != nil {
		return auth.Session{}, err
	}
	dst := filepath.Join(home, "browser-clone")
	w := cmd.ErrOrStderr()
	fmt.Fprintln(w, i18n.T("login.clone.warn", dst))
	if err := browser.CloneProfile(ud, o.fromProfile, dst); err != nil {
		return auth.Session{}, err
	}
	if !o.keepClone {
		defer os.RemoveAll(dst)
	}
	return browserLogin(ctx, w, exe, dst, p)
}

// loginClean: dedicated profile that persists between runs. Needs CDP, so a
// Chromium-family browser.
func loginClean(ctx context.Context, cmd *cobra.Command, p provider.Provider, o loginOpts) (auth.Session, error) {
	home, err := store.Home()
	if err != nil {
		return auth.Session{}, err
	}
	b, err := chooseBrowser(o, true)
	if err != nil {
		return auth.Session{}, err
	}
	return browserLogin(ctx, cmd.ErrOrStderr(), b.Exe, filepath.Join(home, "browser"), p)
}

// chooseBrowser resolves --browser (an id like "edge" or "firefox", or an exe
// path). With no flag it uses the system default browser; on a terminal with
// several installed it asks, default first. chromiumOnly restricts to
// browsers hb can drive over CDP (falling back with a note if the default is
// Firefox-like).
func chooseBrowser(o loginOpts, chromiumOnly bool) (browser.Installation, error) {
	usable := func(b browser.Installation) bool { return !chromiumOnly || b.Kind.Family == browser.Chromium }

	if o.browserSpec != "" {
		b, err := browser.Resolve(o.browserSpec)
		if err != nil {
			return b, err
		}
		if !usable(b) {
			return b, errors.New(i18n.T("login.needchromium", b.Kind.Name))
		}
		return b, nil
	}

	var cands []browser.Installation
	def := browser.DefaultID()
	for _, b := range browser.Installed() {
		if usable(b) {
			cands = append(cands, b)
		}
	}
	if len(cands) == 0 {
		if chromiumOnly {
			return browser.Installation{}, errors.New(i18n.T("login.nochromium"))
		}
		return browser.Installation{}, errors.New("no supported browser found; pass --browser <id|path>")
	}
	// default browser first
	for i, b := range cands {
		if b.Kind.ID == def {
			cands[0], cands[i] = cands[i], cands[0]
			break
		}
	}
	if len(cands) == 1 || !interactive() {
		return cands[0], nil
	}
	opts := make([]choice, len(cands))
	for i, b := range cands {
		label := b.Kind.Name
		if b.Kind.ID == def {
			label += i18n.T("login.default.tag")
		}
		opts[i] = choice{label, b.Kind.ID}
	}
	id, err := selectOne(i18n.T("login.pick.browser"), opts)
	if err != nil {
		return browser.Installation{}, err
	}
	for _, b := range cands {
		if b.Kind.ID == id {
			return b, nil
		}
	}
	return cands[0], nil
}

// loginGecko reads the session straight from a Firefox-family profile.
func loginGecko(cmd *cobra.Command, p provider.Provider, b browser.Installation) (auth.Session, error) {
	dir, err := browser.GeckoProfileDir(b.Kind)
	if err != nil {
		return auth.Session{}, err
	}
	fmt.Fprintln(cmd.ErrOrStderr(), i18n.T("login.gecko.info", b.Kind.Name, dir))
	spec := p.LoginSpec()
	cookies, err := browser.ReadGeckoCookies(dir, spec.CookieURLs, spec.CookieNames)
	if err != nil {
		return auth.Session{}, err
	}
	if len(cookies) == 0 {
		return auth.Session{}, errors.New(i18n.T("login.gecko.none", b.Kind.Name, spec.URL))
	}
	s := auth.Session{Provider: p.Name(), Cookies: cookies}
	if spec.NeedUA {
		// A profile doesn't record its User-Agent; ask for it (cf_clearance is bound to it).
		if s.UserAgent, err = askUserAgent(spec.URL); err != nil && !errors.Is(err, ErrNotInteractive) {
			return auth.Session{}, err
		}
	}
	return s, nil
}

// askUserAgent prompts for the browser's User-Agent (needed for Cloudflare-fronted sites).
func askUserAgent(siteURL string) (string, error) {
	v, err := inputText(i18n.T("login.ua.title"), i18n.T("login.ua.desc", siteURL), false)
	return strings.TrimSpace(v), err
}

// browserLogin opens profileDir in a visible browser, waits until the site
// reports a logged-in user, and harvests the session over CDP.
func browserLogin(ctx context.Context, w io.Writer, exe, profileDir string, p provider.Provider) (auth.Session, error) {
	spec := p.LoginSpec()
	fmt.Fprintln(w, i18n.T("login.opening", p.Name()))
	b, err := browser.Launch(ctx, exe, profileDir, spec.URL)
	if err != nil {
		return auth.Session{}, err
	}
	defer b.Close()

	fmt.Fprintln(w, i18n.T("login.waiting"))
	if err := waitReady(ctx, b, spec.ReadyJS); err != nil {
		return auth.Session{}, err
	}
	cookies, err := b.Cookies(spec.CookieURLs, spec.CookieNames)
	if err != nil {
		return auth.Session{}, err
	}
	if len(cookies) == 0 {
		return auth.Session{}, errors.New("logged in, but no session cookie found")
	}
	s := auth.Session{Provider: p.Name(), Cookies: cookies}
	// Cloudflare-fronted sites bind cf_clearance to the User-Agent.
	if ua, err := b.EvalString("navigator.userAgent"); err == nil {
		s.UserAgent = ua
	}
	if spec.CSRFJS != "" {
		if s.CSRF, err = b.EvalString(spec.CSRFJS); err != nil {
			return auth.Session{}, fmt.Errorf("read CSRF token: %w", err)
		}
	}
	return s, nil
}

// loginToken: the site issues its own personal token; ask for it (masked),
// or read it from stdin, and check its shape before anything is sent.
func loginToken(cmd *cobra.Command, p provider.Provider, t provider.TokenLogin, fromStdin bool) (auth.Session, error) {
	var raw string
	if fromStdin {
		b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 4096))
		if err != nil {
			return auth.Session{}, err
		}
		raw = string(b)
	} else {
		desc := "在" + t.CreateAt + "创建（只显示一次）。输入已隐藏。"
		if t.OnePerKey {
			desc += "每个账号只能有一个令牌，新建会让旧的失效。"
		}
		var err error
		if raw, err = inputText("粘贴 "+p.Name()+" 的个人令牌（"+t.Prefix+"…）", desc, true); err != nil {
			if errors.Is(err, ErrNotInteractive) {
				return auth.Session{}, errors.New("no interactive terminal: pipe the token in with --token-stdin")
			}
			return auth.Session{}, err
		}
	}
	token := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "Bearer "))
	if t.Valid != nil && !t.Valid(token) {
		return auth.Session{}, fmt.Errorf("这不像 %s 的令牌（应以 %s 开头）；请从%s复制完整的令牌", p.Name(), t.Prefix, t.CreateAt)
	}
	return auth.Session{Provider: p.Name(), Token: token}, nil
}

// loginPaste: ask for each cookie value (masked) and build a session.
func loginPaste(ctx context.Context, cmd *cobra.Command, p provider.Provider) (auth.Session, error) {
	spec := p.LoginSpec()
	if len(spec.CookieNames) == 0 || len(spec.CookieURLs) == 0 {
		return auth.Session{}, errors.New("this provider has no fixed cookie names; use another method")
	}
	host, err := hostOf(spec.CookieURLs[0])
	if err != nil {
		return auth.Session{}, err
	}
	var cookies []auth.Cookie
	for _, n := range spec.CookieNames {
		v, err := inputText(
			i18n.T("login.paste.title", n),
			i18n.T("login.paste.desc", spec.URL, n), true)
		if err != nil {
			return auth.Session{}, err
		}
		v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), n+"="))
		if v == "" {
			return auth.Session{}, errors.New("empty cookie value")
		}
		cookies = append(cookies, auth.Cookie{Name: n, Value: v, Domain: host, Path: "/", Secure: true, HTTPOnly: true})
	}
	s := auth.Session{Provider: p.Name(), Cookies: cookies}
	if spec.NeedUA {
		if s.UserAgent, err = askUserAgent(spec.URL); err != nil {
			return auth.Session{}, err
		}
	}
	return s, nil
}

func hostOf(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("bad url %q", raw)
	}
	return u.Hostname(), nil
}

func waitReady(ctx context.Context, b *browser.Browser, js string) error {
	t := time.NewTicker(1500 * time.Millisecond)
	defer t.Stop()
	dead := 0
	for {
		if b.EvalBool(js) {
			return nil
		}
		// Not ready yet: make sure the user hasn't closed the window, so we
		// fail fast instead of waiting out the whole timeout.
		if b.Alive() {
			dead = 0
		} else if dead++; dead >= 2 {
			return errors.New(i18n.T("login.closed"))
		}
		select {
		case <-t.C:
		case <-ctx.Done():
			return fmt.Errorf("login not completed: %w", ctx.Err())
		}
	}
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout <站点>",
		Short: i18n.T("logout.short"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := provider.Get(args[0]); err != nil {
				return err
			}
			prof, err := profileName()
			if err != nil {
				return err
			}
			if err := auth.Delete(prof, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), ui.For(cmd.OutOrStdout()).Mark(true)+" "+i18n.T("logout.done", args[0], prof))
			return nil
		},
	}
}

// shortID is an id as a short tail: a UUID's first eight characters.
func shortID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) > 12 {
		id = id[:8] + "…"
	}
	return "(id " + id + ")"
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami [站点]",
		Short: i18n.T("whoami.short"),
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			names := provider.Names()
			if len(args) == 1 {
				names = args
			}
			prof, err := profileName()
			if err != nil {
				return err
			}
			type row struct {
				Provider string `json:"provider"`
				Username string `json:"username,omitempty"`
				ID       string `json:"id,omitempty"`
				Error    string `json:"error,omitempty"`

				noSession bool // nothing saved for this site: not an error to alarm over
			}
			var rows []row
			for _, n := range names {
				p, err := provider.Get(n)
				if err != nil {
					return err
				}
				r := row{Provider: n}
				s, err := auth.Load(prof, n)
				if err == nil {
					var id provider.Identity
					if id, err = p.Whoami(cmd.Context(), s); err == nil {
						r.Username, r.ID = id.Username, id.ID
					}
				}
				if err != nil {
					r.Error = err.Error()
					r.noSession = errors.Is(err, auth.ErrNoSession)
				}
				rows = append(rows, r)
			}
			out := cmd.OutOrStdout()
			if flagJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(rows)
			}
			u := ui.For(out)
			for _, r := range rows {
				name := u.Bold(ui.Pad(r.Provider, 8))
				switch {
				case r.noSession:
					fmt.Fprintf(out, "%s %s %s\n", name, u.Dim("－ 未登录"), u.Dim("→ hb login "+r.Provider))
				case r.Error != "":
					fmt.Fprintf(out, "%s %s %s\n", name, u.Mark(false), u.Bad(r.Error))
				default:
					fmt.Fprintf(out, "%s %s %s %s\n", name, u.Mark(true), u.Accent(r.Username), u.Dim(shortID(r.ID)))
				}
			}
			return nil
		},
	}
}
