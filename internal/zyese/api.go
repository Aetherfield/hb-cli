package zyese

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// TokenFunc gives a fresh access token (refreshing it first when it is about to expire).
type TokenFunc func(ctx context.Context) (string, error)

// Client calls the OpenAPI. Every request carries the access token; nothing
// else about the person is sent.
type Client struct {
	Base  string
	HTTP  *http.Client
	Token TokenFunc
}

// APIError is an error the API answered with. Code is the machine code: the
// auth layer's error.code, or the one documented for a business detail
// ("unknown_error" when the detail is not one of those; Message then says it).
type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	if hint := e.Hint(); hint != "" {
		return hint
	}
	return fmt.Sprintf("打飞机接口返回 HTTP %d：%s", e.Status, e.Message)
}

// Hint is what to do about it, in words, when the code is a known one.
func (e *APIError) Hint() string {
	switch e.Code {
	case "invalid_token", "token_revoked", "missing_token":
		return "登录已失效或令牌不对，请重新运行 hb zyese login"
	case "insufficient_scope":
		return "令牌缺少 linuxdo 权限：重新运行 hb zyese login，授权时同意 LINUX DO 账号的访问"
	case "linuxdo_binding_required":
		return "你的 Luminet 账号还没有绑定 LINUX DO：到 " + LinkAccountURL + " 绑定，再运行 hb zyese login"
	case "zyese_login_required":
		return "打飞机还没有你这个玩家：先在 " + SiteURL + " 用 LINUX DO 登录一次，再重试"
	case "client_not_approved":
		return "hb 这个应用还没有被打飞机管理员批准，或者已被停用（client_not_approved）；要等管理员处理"
	case "account_banned":
		return "这个账号被封禁了；hb zyese me 可以看到解封时间"
	case "rate_limited":
		if e.RetryAfter > 0 {
			return fmt.Sprintf("请求太频繁，%s 后再试", e.RetryAfter.Round(time.Second))
		}
		return "请求太频繁，稍后再试"
	case "unavailable":
		return "打飞机服务暂时不可用，稍后再试"
	}
	return ""
}

// businessCodes maps the documented business details to their codes, exactly.
var businessCodes = map[string]string{
	"Unauthorized": "missing_token",
	"Link your LinuxDO account at https://auth.luminet.cn/realms/prod/account/account-security/linked-accounts, then refresh your access_token.": "linuxdo_binding_required",
	"Sign in with LinuxDO at https://zyese.xyz once, then retry.":                                                                                "zyese_login_required",
	"This client_id has not been approved or has been disabled. Contact the Zyese administrator.":                                                "client_not_approved",
	"This account is banned. Use GET /openapi/v1/me to check the ban expiry.":                                                                    "account_banned",
	"Too many requests. Please try again later.":                                                                                                 "rate_limited",
	"The service is temporarily unavailable. Please try again later.":                                                                            "unavailable",
}

// parseError reads an error response: the auth layer's {"error":{...}} or a
// business {"detail":"..."}.
func parseError(status int, h http.Header, body []byte) *APIError {
	e := &APIError{Status: status, Code: "unknown_error", Message: http.StatusText(status)}
	if ra, err := strconv.Atoi(h.Get("Retry-After")); err == nil && ra > 0 {
		e.RetryAfter = time.Duration(ra) * time.Second
	}
	var auth struct {
		Error *struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
		Detail json.RawMessage `json:"detail"`
	}
	if json.Unmarshal(body, &auth) == nil {
		switch {
		case auth.Error != nil:
			e.Code, e.Message = auth.Error.Code, auth.Error.Message
		case len(auth.Detail) > 0:
			var d string
			if json.Unmarshal(auth.Detail, &d) == nil {
				e.Message = d
				if c, ok := businessCodes[d]; ok {
					e.Code = c
				}
			} else {
				e.Message = string(auth.Detail) // a validation list, shown as is
			}
		}
	}
	if status == http.StatusTooManyRequests && e.Code == "unknown_error" {
		e.Code = "rate_limited"
	}
	return e
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	tok, err := c.Token(ctx)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("连不上打飞机接口：%w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return parseError(resp.StatusCode, resp.Header, b)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("读不懂打飞机接口的回答：%w", err)
	}
	return nil
}

// Me is GET /me: who the token belongs to, the app, and the input modes.
// The objects are kept whole: the API's fields are shown as they come.
type Me struct {
	User         map[string]any `json:"user"`
	Client       map[string]any `json:"client"`
	Capabilities struct {
		InputModes []string `json:"inputModes"`
	} `json:"capabilities"`
}

func (c *Client) Me(ctx context.Context) (Me, error) {
	var m Me
	return m, c.do(ctx, http.MethodGet, "/me", nil, &m)
}

// Stats is GET /profile/stats, kept as it comes.
func (c *Client) Stats(ctx context.Context) (map[string]any, error) {
	var m map[string]any
	return m, c.do(ctx, http.MethodGet, "/profile/stats", nil, &m)
}

// Leaderboard is GET /leaderboards/{scope}?inputMode=…
func (c *Client) Leaderboard(ctx context.Context, scope, mode string) (map[string]any, error) {
	switch scope {
	case "daily", "yesterday", "all-time":
	default:
		return nil, fmt.Errorf("排行榜只有 daily、yesterday、all-time，没有 %q", scope)
	}
	if err := checkMode(mode); err != nil {
		return nil, err
	}
	var m map[string]any
	return m, c.do(ctx, http.MethodGet, "/leaderboards/"+scope+"?inputMode="+url.QueryEscape(mode), nil, &m)
}

func checkMode(mode string) error {
	switch mode {
	case "en", "zh", "ja":
		return nil
	}
	return fmt.Errorf("模式只有 en、zh、ja，没有 %q", mode)
}

// Run is a new run: what the live connection needs.
type Run struct {
	RunID        string `json:"runId"`
	RunToken     string `json:"runToken"`
	LivePath     string `json:"livePath"`
	RulesVersion any    `json:"rulesVersion"`
}

// StartRun is POST /runs. A new run replaces an unfinished one, so it is only
// called when the person starts a game.
func (c *Client) StartRun(ctx context.Context, mode string) (Run, error) {
	if err := checkMode(mode); err != nil {
		return Run{}, err
	}
	var r Run
	if err := c.do(ctx, http.MethodPost, "/runs", map[string]string{"inputMode": mode}, &r); err != nil {
		return Run{}, err
	}
	if r.RunID == "" || r.RunToken == "" || r.LivePath == "" {
		return Run{}, errors.New("开局的回答缺少 runId、runToken 或 livePath")
	}
	return r, nil
}

// RunState is GET /runs/{id}, kept as it comes; Result is null until the
// server has settled the run.
func (c *Client) RunState(ctx context.Context, id string) (map[string]any, error) {
	var m map[string]any
	return m, c.do(ctx, http.MethodGet, "/runs/"+url.PathEscape(id), nil, &m)
}

// Abandon ends a run the person left.
func (c *Client) Abandon(ctx context.Context, r Run, reason string) error {
	return c.do(ctx, http.MethodPost, "/runs/"+url.PathEscape(r.RunID)+"/abandon",
		map[string]string{"runToken": r.RunToken, "reason": clip(reason, 120)}, nil)
}

// ConnectionFailed gives up a run whose first connection never worked.
func (c *Client) ConnectionFailed(ctx context.Context, r Run, reason string) error {
	return c.do(ctx, http.MethodPost, "/runs/"+url.PathEscape(r.RunID)+"/connection-failed",
		map[string]string{"runToken": r.RunToken, "reason": clip(reason, 120)}, nil)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
