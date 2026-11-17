// Package zyese talks to 打飞机 (zyese.xyz)'s OpenAPI: sign-in through
// Luminet Auth (OIDC, authorization code + PKCE), the HTTP API, and a run's
// live WebSocket. The game runs on the server; this side only shows it and
// forwards the keys a person types. It never makes up input, never reports a
// score, and never solves anything for the player.
package zyese

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	// Issuer is Luminet Auth's realm; everything else comes from its discovery document.
	Issuer = "https://auth.luminet.cn/realms/prod"
	// ClientID is hb's public client, registered by Luminet for it.
	ClientID = "app_MWOteMtkZW1LGzdoZiF2"
	// RedirectURI is the callback registered for that client. The listener is
	// on the loopback addresses only, never on all interfaces.
	RedirectURI = "http://localhost:1423/callback"
	// APIBase is the game's OpenAPI.
	APIBase = "https://api-zyese.luminet.cn/openapi/v1"
	// LinkAccountURL is where a Luminet account is linked to LINUX DO.
	LinkAccountURL = "https://auth.luminet.cn/realms/prod/account/account-security/linked-accounts"
	// SiteURL is the game's site, where a LINUX DO player is first created.
	SiteURL = "https://zyese.xyz"
)

// Scopes asks for the LINUX DO link (the API finds the player by it) and a
// refresh token.
var Scopes = []string{oidc.ScopeOpenID, "email", oidc.ScopeOfflineAccess, "linuxdo"}

// Tokens is what a sign-in or a refresh yields. Only Access is sent to the
// game; Refresh only to the IdP.
type Tokens struct {
	Access  string
	Refresh string
	Expiry  time.Time
}

// ErrCancelled: the person said no on the sign-in page, or gave up.
var ErrCancelled = errors.New("登录取消了")

// ErrReauth: the refresh token no longer works; sign in again.
var ErrReauth = errors.New("登录已失效，请重新运行 hb zyese login")

// Login runs one sign-in: it listens on the registered loopback callback
// first, then opens the system browser on the authorization page, waits for
// the one callback that matches this attempt, exchanges the code and checks
// the ID token (signature, issuer, audience, expiry, nonce).
//
// open opens a URL in the browser (nil: the system's). say receives the lines
// to show the person (the address to open by hand if the browser does not).
func Login(ctx context.Context, hc *http.Client, open func(string) error, say io.Writer) (Tokens, error) {
	ctx = oidc.ClientContext(ctx, hc)
	provider, err := discover(ctx)
	if err != nil {
		return Tokens{}, fmt.Errorf("读 Luminet Auth 的配置失败：%w", err)
	}
	conf := oauthConfig(provider.Endpoint())

	cb, err := listenCallback()
	if err != nil {
		return Tokens{}, err
	}
	defer cb.close()

	state, nonce, verifier := oauth2.GenerateVerifier(), oauth2.GenerateVerifier(), oauth2.GenerateVerifier()
	cb.expect(state)
	authURL := conf.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce))

	if open == nil {
		open = OpenBrowser
	}
	fmt.Fprintln(say, "正在浏览器里打开 Luminet 的登录页；没打开的话，手动打开这个地址：")
	fmt.Fprintln(say, authURL)
	if err := open(authURL); err != nil {
		fmt.Fprintf(say, "（打开浏览器失败：%v）\n", err)
	}

	wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var code string
	select {
	case r := <-cb.result:
		if r.err != nil {
			return Tokens{}, r.err
		}
		code = r.code
	case <-wait.Done():
		if ctx.Err() != nil {
			return Tokens{}, ErrCancelled
		}
		return Tokens{}, errors.New("5 分钟内没有完成登录，已放弃；再运行一次 hb zyese login")
	}
	cb.close()

	tok, err := conf.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Tokens{}, fmt.Errorf("用授权码换令牌失败：%w", oauthError(err))
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return Tokens{}, errors.New("Luminet Auth 没有返回 ID Token，不能确认登录")
	}
	idt, err := provider.Verifier(&oidc.Config{ClientID: ClientID}).Verify(ctx, raw)
	if err != nil {
		return Tokens{}, fmt.Errorf("ID Token 校验失败，登录作废：%w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(nonce)) != 1 {
		return Tokens{}, errors.New("ID Token 的 nonce 对不上，登录作废")
	}
	return Tokens{Access: tok.AccessToken, Refresh: tok.RefreshToken, Expiry: tok.Expiry}, nil
}

// Refresh trades the refresh token for new tokens. A refresh token the IdP
// returns replaces the old one; a rejected one means signing in again.
func Refresh(ctx context.Context, hc *http.Client, refresh string) (Tokens, error) {
	if refresh == "" {
		return Tokens{}, ErrReauth
	}
	ctx = oidc.ClientContext(ctx, hc)
	provider, err := discover(ctx)
	if err != nil {
		return Tokens{}, fmt.Errorf("读 Luminet Auth 的配置失败：%w", err)
	}
	conf := oauthConfig(provider.Endpoint())
	tok, err := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: refresh, Expiry: time.Unix(1, 0)}).Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.ErrorCode == "invalid_grant" {
			return Tokens{}, ErrReauth
		}
		return Tokens{}, fmt.Errorf("刷新令牌失败：%w", oauthError(err))
	}
	out := Tokens{Access: tok.AccessToken, Refresh: tok.RefreshToken, Expiry: tok.Expiry}
	if out.Refresh == "" {
		out.Refresh = refresh
	}
	return out, nil
}

// discover reads the IdP's discovery document; it is a plain GET, so a
// network hiccup (a TLS handshake timing out) is tried again, three times at most.
func discover(ctx context.Context) (*oidc.Provider, error) {
	var err error
	for i := 0; i < 3; i++ {
		var p *oidc.Provider
		if p, err = oidc.NewProvider(ctx, Issuer); err == nil {
			return p, nil
		}
		select {
		case <-time.After(time.Duration(i+1) * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, err
}

func oauthConfig(ep oauth2.Endpoint) oauth2.Config {
	ep.AuthStyle = oauth2.AuthStyleInParams // a public client: client_id in the body, no secret
	return oauth2.Config{ClientID: ClientID, Endpoint: ep, RedirectURL: RedirectURI, Scopes: Scopes}
}

// oauthError keeps the IdP's error code and description, never the response body as a whole.
func oauthError(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		msg := re.ErrorCode
		if re.ErrorDescription != "" {
			msg += "：" + re.ErrorDescription
		}
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", re.Response.StatusCode)
		}
		return errors.New(msg)
	}
	return err
}

// ---- the loopback callback ----

type cbResult struct {
	code string
	err  error
}

type callback struct {
	servers []*http.Server
	result  chan cbResult
	mu      sync.Mutex
	state   string
	done    bool
	once    sync.Once
}

// listenCallback listens on 127.0.0.1 and ::1 at the registered port (a
// browser may resolve localhost to either). It never listens on all interfaces.
func listenCallback() (*callback, error) {
	u, _ := url.Parse(RedirectURI)
	port := u.Port()
	cb := &callback{result: make(chan cbResult, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc(u.Path, cb.handle)
	var firstErr error
	for _, host := range []string{"127.0.0.1", "::1"} {
		l, err := net.Listen("tcp", net.JoinHostPort(host, port))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		cb.servers = append(cb.servers, srv)
		go func() { _ = srv.Serve(l) }()
	}
	if len(cb.servers) == 0 {
		return nil, fmt.Errorf("监听登录回调 %s 失败（端口 %s 被占用？）：%w", RedirectURI, port, firstErr)
	}
	return cb, nil
}

func (cb *callback) expect(state string) {
	cb.mu.Lock()
	cb.state = state
	cb.mu.Unlock()
}

func (cb *callback) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cb.mu.Lock()
	want, done := cb.state, cb.done
	ok := !done && want != "" && subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(want)) == 1
	if ok {
		cb.done = true // one callback per attempt: a repeat is refused
	}
	cb.mu.Unlock()
	if !ok {
		http.Error(w, "这个回调不属于当前的登录（重复、过期或不匹配），已忽略。", http.StatusBadRequest)
		return
	}
	var res cbResult
	switch {
	case q.Get("error") == "access_denied":
		res.err = ErrCancelled
	case q.Get("error") != "":
		res.err = fmt.Errorf("Luminet Auth 返回错误：%s %s", q.Get("error"), q.Get("error_description"))
	case q.Get("code") == "":
		res.err = errors.New("回调里没有授权码")
	default:
		res.code = q.Get("code")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	msg := "登录完成，可以关掉这个页面回到终端了。"
	if res.err != nil {
		msg = "登录没有完成：" + res.err.Error()
	}
	fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>hb</title><p style=\"font:16px system-ui;margin:3em\">%s</p>", html.EscapeString(msg))
	cb.result <- res
}

func (cb *callback) close() {
	cb.once.Do(func() {
		for _, s := range cb.servers {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = s.Shutdown(ctx)
			cancel()
		}
	})
}

// OpenBrowser opens a URL in the system's browser.
func OpenBrowser(u string) error {
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return errors.New("not a web address")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}
