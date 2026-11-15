// Package unsnow is the provider for hb.unsnow.org, through its official,
// read-only CLI API (https://hb.unsnow.org/openapi.json, "CLI 只读 API",
// v1.1.0 of 2026-11-15). It authenticates with the account's own personal
// token (Authorization: Bearer hbr_…), made on the web page 我的 → 认证令牌.
//
// The API's rules, which this package keeps: only requests a person asks for,
// one at a time; never guess or enumerate passcodes; no polling, loops or
// automatic retries (429, 401, 403, 404, 410 and 5xx all stop here and say
// why). Claiming is not part of the API: the user opens the packet's link in
// a browser and claims there, past the site's Turnstile check.
package unsnow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"hb/internal/auth"
	"hb/internal/httpx"
	"hb/internal/provider"
)

const (
	Base = "https://hb.unsnow.org"
	// apiPrefix is the CLI API's root.
	apiPrefix = "/api/cli/v1"
	// TokenPage is where a person makes or revokes their token.
	TokenPage = Base + "/"
	// VerificationURL is where the passcode-lookup check is done, in a browser.
	VerificationURL = Base + "/?cli_verify=1"
)

// tokenRe is the token's shape: hbr_ and 43 base64url characters.
var tokenRe = regexp.MustCompile(`^hbr_[A-Za-z0-9_-]{43}$`)

// ValidToken says whether s looks like an Unsnow personal token.
func ValidToken(s string) bool { return tokenRe.MatchString(s) }

func init() { provider.Register(&Unsnow{BaseURL: Base, HTTP: newClient()}) }

// newClient never retries: the API forbids automatic retries, 5xx included.
func newClient() *httpx.Client {
	c := httpx.New()
	c.MaxRetries = 0
	return c
}

type Unsnow struct {
	BaseURL string
	HTTP    *httpx.Client
}

func (*Unsnow) Name() string { return "unsnow" }

func (*Unsnow) LoginSpec() provider.LoginSpec {
	return provider.LoginSpec{
		URL: Base + "/",
		Token: &provider.TokenLogin{
			Prefix:    "hbr_",
			CreateAt:  "网页「我的 → 认证令牌」（" + TokenPage + "）",
			Valid:     ValidToken,
			OnePerKey: true,
		},
	}
}

// APIError is the site's refusal, in its own words where it gave them.
type APIError struct {
	Status          int
	Code            string // e.g. lookup_captcha_required, code_not_found, invalid_history_query
	Message         string
	RetryAfter      time.Duration // 429: how long to wait (at least a second)
	VerificationURL string        // 403 lookup_captcha_required: where to do the check
	LookupCounted   bool          // this request counted as a miss
	NotJSON         bool          // the answer was not the API's JSON (a network-level block, say)
}

func (e *APIError) Error() string {
	switch {
	case e.NotJSON:
		return fmt.Sprintf("unsnow: HTTP %d，回复不是接口的 JSON（可能被网络层风控拦下了），稍后自己再试", e.Status)
	case e.Code == "lookup_captcha_required":
		u := e.VerificationURL
		if u == "" {
			u = VerificationURL
		}
		return "unsnow: 口令查错次数太多，需要先在网页完成查询验证：用这个令牌的账号打开 " + u +
			" 点「完成口令查询验证」，然后再手动重试（验证完成前所有查询都会被拒绝）"
	case e.Status == http.StatusTooManyRequests:
		return fmt.Sprintf("unsnow: 查询太频繁，等 %s 后再自己重试", e.RetryAfter.Round(time.Second))
	case e.Status == http.StatusUnauthorized:
		return "unsnow: 令牌无效或已撤销，到网页「我的 → 认证令牌」重新创建，再运行 `hb login unsnow`"
	}
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.Code != "" {
		return fmt.Sprintf("unsnow: %s（%s，HTTP %d）", msg, e.Code, e.Status)
	}
	return fmt.Sprintf("unsnow: %s（HTTP %d）", msg, e.Status)
}

// get asks one endpoint and decodes its answer into out. One request, never
// retried; a refusal comes back as *APIError.
func (u *Unsnow) get(ctx context.Context, s auth.Session, path string, query url.Values, out any) error {
	if s.Token == "" {
		return errors.New("unsnow: 还没有令牌，运行 `hb login unsnow`")
	}
	target := u.BaseURL + apiPrefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	s.Apply(req)
	req.Header.Set("Accept", "application/json")
	resp, err := u.HTTP.Do(ctx, req)
	if err != nil {
		var rl *httpx.RateLimitedError
		if errors.As(err, &rl) {
			return &APIError{Status: http.StatusTooManyRequests, RetryAfter: max(rl.RetryAfter, time.Second)}
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	isJSON := strings.Contains(resp.Header.Get("Content-Type"), "json") && json.Valid(body)
	if resp.StatusCode == http.StatusOK {
		if !isJSON {
			return &APIError{Status: resp.StatusCode, NotJSON: true}
		}
		return json.Unmarshal(body, out)
	}
	if !isJSON {
		return &APIError{Status: resp.StatusCode, NotJSON: true}
	}
	var e struct {
		Error           string `json:"error"`
		Code            string `json:"code"`
		RetryAfter      int    `json:"retry_after"`
		VerificationURL string `json:"verification_url"`
		LookupCounted   bool   `json:"lookup_counted"`
	}
	_ = json.Unmarshal(body, &e)
	apiErr := &APIError{Status: resp.StatusCode, Code: e.Code, Message: e.Error,
		VerificationURL: e.VerificationURL, LookupCounted: e.LookupCounted}
	if resp.StatusCode == http.StatusTooManyRequests {
		apiErr.RetryAfter = max(time.Duration(e.RetryAfter)*time.Second, time.Second)
	}
	return apiErr
}

// ClaimSecurity is the account's passcode-lookup standing. Misses are shared
// with the web page; at Threshold a Google reCAPTCHA check is due.
type ClaimSecurity struct {
	VerificationRequired bool  `json:"verification_required"`
	ConsecutiveMisses    int   `json:"consecutive_misses"`
	Threshold            int   `json:"threshold"`
	ServerNow            int64 `json:"server_now"`
	BannedUntil          int64 `json:"banned_until"` // 0 in the current reCAPTCHA mode
}

// Me is GET /api/cli/v1/me: the token's account and its lookup standing.
type Me struct {
	User struct {
		LinuxDoID  int64   `json:"linuxdo_id"`
		Username   string  `json:"username"`
		Name       *string `json:"name"`
		TrustLevel *int    `json:"trust_level"`
	} `json:"user"`
	Scope           string        `json:"scope"`
	ClaimSecurity   ClaimSecurity `json:"claim_security"`
	VerificationURL string        `json:"verification_url"`
}

// GetMe reads the token's account. It works while a lookup check is due.
func (u *Unsnow) GetMe(ctx context.Context, s auth.Session) (Me, error) {
	var me Me
	err := u.get(ctx, s, "/me", nil, &me)
	return me, err
}

// Whoami implements provider.Provider.
func (u *Unsnow) Whoami(ctx context.Context, s auth.Session) (provider.Identity, error) {
	me, err := u.GetMe(ctx, s)
	if err != nil {
		return provider.Identity{}, err
	}
	return provider.Identity{ID: strconv.FormatInt(me.User.LinuxDoID, 10), Username: me.User.Username}, nil
}
