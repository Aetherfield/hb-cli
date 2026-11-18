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
	"strings"
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
	case "not_found":
		return "打飞机说找不到这个东西（对局可能已经不在了）"
	case "invalid_request":
		return "打飞机说这个请求不对（多半是 hb 的问题，请告诉维护者）"
	case "state_conflict":
		return "和对局现在的状态冲突（比如这局已经结束了）"
	case "mode_unsupported":
		return "这里不支持这个模式"
	case "scope_unsupported":
		return "不支持这个排行榜范围（活动榜只有 en、zh）"
	case "hell_locked":
		return "这个地狱模式还没解锁：hb zyese hell 可以看解锁条件"
	case "hell_no_attempts":
		return "今天的地狱模式次数用完了：hb zyese hell 可以看什么时候重置"
	case "linuxdo_unavailable":
		return "这个 LINUX DO 账号现在不可用"
	case "forbidden":
		return "没有权限访问这个"
	case "websocket_required":
		return "这里需要 WebSocket 连接（多半是 hb 的问题，请告诉维护者）"
	case "session_expired":
		return "这局已经过期了，不能继续"
	case "method_not_allowed":
		return "打飞机不支持这种请求方式（多半是 hb 的问题，请告诉维护者）"
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
	// the rest of DetailError's texts in openapi-v1.yaml
	"The requested resource was not found.":                                                               "not_found",
	"The request is invalid.":                                                                             "invalid_request",
	"The operation conflicts with the current game state.":                                                "state_conflict",
	"The requested input mode is not supported here.":                                                     "mode_unsupported",
	"The requested leaderboard scope is not supported.":                                                   "scope_unsupported",
	"This hell mode is locked. Check GET /openapi/v1/profile/hell-attempts.":                              "hell_locked",
	"No hell mode attempts remain today. Check GET /openapi/v1/profile/hell-attempts for the reset time.": "hell_no_attempts",
	"This LinuxDO account is currently unavailable.":                                                      "linuxdo_unavailable",
	"You do not have permission to access this resource.":                                                 "forbidden",
	"A WebSocket connection is required.":                                                                 "websocket_required",
	"This game session has expired.":                                                                      "session_expired",
	"This HTTP method is not supported for this resource.":                                                "method_not_allowed",
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
	// A GET only reads, so a network hiccup (a TLS handshake timing out) is
	// tried again, twice at most; a POST (starting or leaving a run) never is.
	var resp *http.Response
	for try := 0; ; try++ {
		resp, err = c.HTTP.Do(req)
		if err == nil || method != http.MethodGet || try == 2 || ctx.Err() != nil {
			break
		}
		select {
		case <-time.After(time.Duration(try+1) * 700 * time.Millisecond):
		case <-ctx.Done():
		}
	}
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
	case "event":
		if mode != "en" && mode != "zh" {
			return nil, errors.New("活动榜只有 en、zh 两个模式")
		}
	default:
		return nil, fmt.Errorf("排行榜只有 daily、yesterday、all-time、event，没有 %q", scope)
	}
	if err := checkMode(mode); err != nil {
		return nil, err
	}
	var m map[string]any
	return m, c.do(ctx, http.MethodGet, "/leaderboards/"+scope+"?inputMode="+url.QueryEscape(mode), nil, &m)
}

// Modes are the typing games hb plays: normal en, zh, ja and the hell modes.
// (Roguelike expeditions are a different game; hb does not play them.)
var Modes = []string{"en", "zh", "ja", "hell-en", "hell-zh"}

func checkMode(mode string) error {
	for _, m := range Modes {
		if m == mode {
			return nil
		}
	}
	return fmt.Errorf("模式只有 %s，没有 %q", strings.Join(Modes, "、"), mode)
}

// IsHell says whether a mode is a hell mode (unlocked by play, limited per day).
func IsHell(mode string) bool { return strings.HasPrefix(mode, "hell-") }

// HellAttempts is one hell mode's unlock progress and today's attempts.
type HellAttempts struct {
	InputMode      string `json:"inputMode"`
	NormalMode     string `json:"normalMode"`
	Used           int    `json:"used"`
	Remaining      int    `json:"remaining"` // -1: unlimited
	Limit          int    `json:"limit"`     // -1: unlimited
	BestKills      int    `json:"bestKills"`
	CompletedWaves int    `json:"completedWaves"`
	RequiredKills  int    `json:"requiredKills"`
	RequiredWaves  int    `json:"requiredWaves"`
	ResetAt        int64  `json:"resetAt"` // Unix ms
	Unlocked       bool   `json:"unlocked"`
	Unlimited      bool   `json:"unlimited"`
}

// Hell is GET /profile/hell-attempts.
func (c *Client) Hell(ctx context.Context) (map[string]HellAttempts, error) {
	var r struct {
		Attempts map[string]HellAttempts `json:"attempts"`
	}
	return r.Attempts, c.do(ctx, http.MethodGet, "/profile/hell-attempts", nil, &r)
}

// Event is the active typing event, or nil.
type Event struct {
	ID       int    `json:"id"`
	Title    string `json:"title"`
	StartsAt int64  `json:"startsAt"`
	EndsAt   int64  `json:"endsAt"`
}

// ActiveEvent is GET /events/active.
func (c *Client) ActiveEvent(ctx context.Context) (*Event, error) {
	var r struct {
		Event *Event `json:"event"`
	}
	return r.Event, c.do(ctx, http.MethodGet, "/events/active", nil, &r)
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
