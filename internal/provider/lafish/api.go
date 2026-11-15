package lafish

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"hb/internal/auth"
)

// APIError is lafish's `{error:{code,message}}` answer.
type APIError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration // from Retry-After on 429
	Body       json.RawMessage
	ByToken    bool // the request carried a personal token, not the cookie
}

func (e *APIError) Error() string {
	if e.ByToken && e.Code == "INVALID_PERSONAL_TOKEN" {
		return "lafish: 令牌不对、已过期或已停用；到「我的 → 个人令牌」重新创建，再运行 `hb login lafish`"
	}
	switch e.Code {
	case "PERSONAL_TOKENS_PAUSED": // the 站长's switch: the page still works, and the token is still good
		return "lafish: 站长暂时关了个人令牌，请先在网页上操作；令牌本身还有效，重新开放后接着能用"
	case "TOKEN_CLAIMS_OFF": // the sender's choice, per 红包
		return "lafish: 这个红包的发放者不允许用个人令牌领取（也不能用令牌进它的附带网页），请在网页上领"
	}
	if e.ByToken && e.Code == "PERSONAL_TOKEN_NOT_ALLOWED" {
		return "lafish: 这个操作令牌做不了（令牌只管查看、领取、口令兑换和附带网页认人），请在网页上操作"
	}
	return fmt.Sprintf("lafish: HTTP %d %s: %s", e.Status, e.Code, e.Message)
}

func (l *Lafish) apiBase() string {
	if l.API != "" {
		return l.API
	}
	return Origin + "/api"
}

func (l *Lafish) origin() string {
	if l.OriginHeader != "" {
		return l.OriginHeader
	}
	return Origin
}

// do sends a request with the session. Mutations carry the exact Origin and
// the CSRF token, as the site's own pages do (requireProtectedMutation).
func (l *Lafish) do(ctx context.Context, s auth.Session, method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, l.apiBase()+path, rdr)
	if err != nil {
		return nil, err
	}
	s.Apply(req)
	req.Header.Set("Accept", "application/json")
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
		// Origin and CSRF guard the cookie session; a personal token is not a
		// cookie, so the site asks neither of it.
		if s.Token == "" {
			req.Header.Set("Origin", l.origin())
			if s.CSRF != "" {
				req.Header.Set("X-CSRF-Token", s.CSRF)
			}
		}
	}
	resp, err := l.HTTP.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, nil
	}
	ae := &APIError{Status: resp.StatusCode, Body: raw, ByToken: s.Token != ""}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil {
		ae.Code, ae.Message = env.Error.Code, env.Error.Message
	}
	if ae.Code == "" {
		ae.Code = "HTTP_" + strconv.Itoa(resp.StatusCode)
	}
	if v := resp.Header.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ae.RetryAfter = time.Duration(n) * time.Second
		}
	}
	return nil, ae
}

// Activity is the public read of a packet (GET /activities/:id). Only fields
// hb needs are typed; Raw keeps the rest. Fields marked "unconfirmed" in
// docs/api-notes.md are decoded loosely.
type Activity struct {
	ID                string          `json:"id"`
	Title             string          `json:"title"`
	Status            string          `json:"status"`
	DeadlineAt        string          `json:"deadlineAt"`
	OpenedAt          string          `json:"openedAt"`
	HumanCheck        bool            `json:"humanCheck"`
	HumanCheckMethod  string          `json:"humanCheckMethod"`
	ParticipationMode string          `json:"participationMode"`
	Page              json.RawMessage `json:"page"`  // AttachedPage: shape unconfirmed (an object with a URL, or a string)
	Judge             map[string]any  `json:"judge"` // PublicJudge: fingerprint, judged, unclaimed, view, ...
	Raw               json.RawMessage `json:"-"`
}

// PageURL extracts the attached page's address from Page, whatever its shape.
func (a Activity) PageURL() string {
	if len(a.Page) == 0 || string(a.Page) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(a.Page, &s) == nil {
		return s
	}
	var o map[string]any
	if json.Unmarshal(a.Page, &o) == nil {
		for _, k := range []string{"url", "href", "address"} {
			if v, ok := o[k].(string); ok && v != "" {
				return v
			}
		}
	}
	return ""
}

// GetActivity reads a packet. The session is optional (public read) but adds
// personal fields.
func (l *Lafish) GetActivity(ctx context.Context, s auth.Session, id string) (Activity, error) {
	raw, err := l.do(ctx, s, http.MethodGet, "/activities/"+id, nil)
	if err != nil {
		return Activity{}, err
	}
	var env struct {
		Activity json.RawMessage `json:"activity"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || len(env.Activity) == 0 {
		return Activity{}, errors.New("lafish: unexpected /activities/:id answer")
	}
	var a Activity
	if err := json.Unmarshal(env.Activity, &a); err != nil {
		return Activity{}, err
	}
	a.Raw = env.Activity
	return a, nil
}

// ClaimCode submits one input to a 代码判定 packet (participationMode "code").
// The answer is returned raw (ClaimResult: shape for code mode unconfirmed).
// humanToken is only sent when the packet asked for a human check.
func (l *Lafish) ClaimCode(ctx context.Context, s auth.Session, id, input, humanToken string) (json.RawMessage, error) {
	body := map[string]any{
		"submissionId":      newUUID(),
		"participationMode": "code",
		"input":             input,
	}
	if humanToken != "" {
		body["humanToken"] = humanToken
	}
	return l.do(ctx, s, http.MethodPost, "/activities/"+id+"/claim", body)
}

// ClaimDirect takes the share of a packet that is claimed with one click
// (participationMode "direct"): no passcode, no input, so there is nothing to
// guess. One request, never retried. A packet that wants a human check answers
// HUMAN_CHECK_REQUIRED and hb stops there (hb never solves one).
func (l *Lafish) ClaimDirect(ctx context.Context, s auth.Session, id string) (json.RawMessage, error) {
	return l.do(ctx, s, http.MethodPost, "/activities/"+id+"/claim", map[string]any{
		"submissionId":      newUUID(),
		"participationMode": "direct",
	})
}

// PageIdentity asks lafish to sign a token naming the viewer for this
// packet's page (docs: page-identity-design). nonce is optional.
func (l *Lafish) PageIdentity(ctx context.Context, s auth.Session, id, nonce string) (json.RawMessage, error) {
	body := map[string]any{}
	if nonce != "" {
		body["nonce"] = nonce
	}
	return l.do(ctx, s, http.MethodPost, "/activities/"+id+"/page-identity", body)
}

// newUUID returns a random (v4) UUID string.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
