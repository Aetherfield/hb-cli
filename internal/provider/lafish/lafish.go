// Package lafish is the provider for hb.lafi.sh (LeUKi/hb).
//
// Claim/plaza/history/balance use the browser session (cookie hb_session +
// Origin + X-CSRF-Token); see docs in LeUKi/hb apps/api/src/auth/session.ts.
package lafish

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"hb/internal/auth"
	"hb/internal/httpx"
	"hb/internal/provider"
)

const (
	Origin     = "https://hb.lafi.sh"
	sessionURL = Origin + "/api/session"
)

func init() { provider.Register(&Lafish{BaseURL: sessionURL, HTTP: httpx.New()}) }

type Lafish struct {
	BaseURL string // session endpoint; overridden in tests
	HTTP    *httpx.Client

	// API is the API base (default https://hb.lafi.sh/api) and OriginHeader the
	// Origin sent on mutations (default https://hb.lafi.sh); tests override both.
	API          string
	OriginHeader string
}

func (*Lafish) Name() string { return "lafish" }

func (*Lafish) LoginSpec() provider.LoginSpec {
	return provider.LoginSpec{
		URL:         Origin + "/",
		ReadyJS:     `fetch('/api/session',{credentials:'include'}).then(r=>r.json()).then(j=>!!(j&&j.user)).catch(()=>false)`,
		CSRFJS:      `fetch('/api/session',{credentials:'include'}).then(r=>r.json()).then(j=>(j&&j.csrfToken)||'')`,
		CookieURLs:  []string{Origin + "/"},
		CookieNames: []string{"hb_session"},
		CSRFURL:     sessionURL,
		CSRFField:   "csrfToken",
	}
}

// FetchCSRF gets the CSRF token for a saved/pasted session cookie.
func (l *Lafish) FetchCSRF(ctx context.Context, s auth.Session) (string, error) {
	r, err := l.fetchSession(ctx, s)
	if err != nil {
		return "", err
	}
	if r.User == nil {
		return "", fmt.Errorf("cookie is not logged in")
	}
	return r.CSRFToken, nil
}

// sessionResp is GET /api/session. Only fields we rely on are typed; the
// user object's exact shape is unconfirmed, so it is decoded loosely.
type sessionResp struct {
	User      map[string]any `json:"user"`
	CSRFToken string         `json:"csrfToken"`
}

func (l *Lafish) fetchSession(ctx context.Context, s auth.Session) (sessionResp, error) {
	req, err := http.NewRequest(http.MethodGet, l.BaseURL, nil)
	if err != nil {
		return sessionResp{}, err
	}
	s.Apply(req)
	req.Header.Set("Accept", "application/json")
	resp, err := l.HTTP.Do(ctx, req)
	if err != nil {
		return sessionResp{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return sessionResp{}, fmt.Errorf("lafish /session: HTTP %d", resp.StatusCode)
	}
	var out sessionResp
	if err := json.Unmarshal(body, &out); err != nil {
		return sessionResp{}, fmt.Errorf("lafish /session: bad JSON: %w", err)
	}
	return out, nil
}

// Whoami asks the server who the saved cookie belongs to.
func (l *Lafish) Whoami(ctx context.Context, s auth.Session) (provider.Identity, error) {
	r, err := l.fetchSession(ctx, s)
	if err != nil {
		return provider.Identity{}, err
	}
	if r.User == nil {
		return provider.Identity{}, fmt.Errorf("not logged in (session expired?); run `hb login lafish`")
	}
	return provider.Identity{ID: str(r.User["id"]), Username: firstNonEmpty(str(r.User["username"]), str(r.User["name"]))}, nil
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return fmt.Sprintf("%.0f", x)
	}
	return ""
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
