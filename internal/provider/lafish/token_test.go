package lafish

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hb/internal/auth"
	"hb/internal/httpx"
)

// A 个人令牌 (LeUKi/hb docs/personal-token-design.md): the account without the
// cookie. Authorization: Bearer, and none of what only guards a cookie.

const tokenText = "hbp_0123456789abcdefghijklmnopqrstuv"

func tokenSess() auth.Session { return auth.Session{Provider: "lafish", Token: tokenText} }

func TestTokenShape(t *testing.T) {
	if !ValidToken(tokenText) {
		t.Fatal("a well-formed token")
	}
	for _, bad := range []string{"", "hbp_short", "hbk_0123456789abcdefghijklmnopqrstuv", tokenText + "x", "hbp_0123456789abcdefghijklmnopqrstu!"} {
		if ValidToken(bad) {
			t.Errorf("%q should not pass", bad)
		}
	}
}

func TestATokenSendsTheBearerAndNothingThatGuardsACookie(t *testing.T) {
	var gotPost, gotGet http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			gotPost = r.Header.Clone()
			w.Write([]byte(`{"result":"judged"}`))
			return
		}
		gotGet = r.Header.Clone()
		w.Write([]byte(`{"user":{"id":"u1","username":"alice"},"csrfToken":null}`))
	}))
	defer srv.Close()
	l := &Lafish{BaseURL: srv.URL + "/api/session", API: srv.URL + "/api", HTTP: httpx.New()}
	ctx := context.Background()

	id, err := l.Whoami(ctx, tokenSess())
	if err != nil || id.Username != "alice" {
		t.Fatalf("%+v %v", id, err)
	}
	if _, err := l.ClaimCode(ctx, tokenSess(), "act1", "hello", ""); err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]http.Header{"read": gotGet, "claim": gotPost} {
		if h.Get("Authorization") != "Bearer "+tokenText {
			t.Errorf("%s: Authorization %q", name, h.Get("Authorization"))
		}
		if h.Get("Cookie") != "" {
			t.Errorf("%s: a token must not ride with a cookie: %q", name, h.Get("Cookie"))
		}
	}
	if gotPost.Get("Origin") != "" || gotPost.Get("X-CSRF-Token") != "" {
		t.Errorf("a claim by token carries no Origin or CSRF: %v", gotPost)
	}
}

func TestACookieSessionStillCarriesOriginAndCSRF(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Write([]byte(`{"result":"judged"}`))
	}))
	defer srv.Close()
	l := &Lafish{API: srv.URL + "/api", HTTP: httpx.New()}
	if _, err := l.ClaimCode(context.Background(), sess(), "act1", "hello", ""); err != nil {
		t.Fatal(err)
	}
	if got.Get("Origin") == "" || got.Get("X-CSRF-Token") != "csrf-tok" || got.Get("Authorization") != "" || !strings.Contains(got.Get("Cookie"), "hb_session=abc") {
		t.Fatalf("the cookie path is as it was: %v", got)
	}
}

func TestATokenTheSiteDoesNotKnowIsExplained(t *testing.T) {
	// A site without personal tokens ignores the header: the request is anonymous.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"user":null,"csrfToken":null,"attemptCooldown":null}`))
	}))
	defer srv.Close()
	l := &Lafish{BaseURL: srv.URL + "/api/session", HTTP: httpx.New()}
	_, err := l.Whoami(context.Background(), tokenSess())
	if err == nil || !strings.Contains(err.Error(), "个人令牌") {
		t.Fatalf("want a hint about tokens, got %v", err)
	}
}

func TestTokenRefusalsAreReadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/activities/a/claim":
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"code":"INVALID_PERSONAL_TOKEN","message":"令牌不对、已过期或已停用"}}`))
		default:
			w.WriteHeader(403)
			w.Write([]byte(`{"error":{"code":"PERSONAL_TOKEN_NOT_ALLOWED","message":"令牌只能在网页上管理"}}`))
		}
	}))
	defer srv.Close()
	l := &Lafish{API: srv.URL + "/api", HTTP: httpx.New()}
	_, err := l.ClaimCode(context.Background(), tokenSess(), "a", "x", "")
	if err == nil || !strings.Contains(err.Error(), "hb login lafish") {
		t.Fatalf("revoked or run-out token: %v", err)
	}
	_, err = l.do(context.Background(), tokenSess(), http.MethodPost, "/me/personal-tokens", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "令牌做不了") {
		t.Fatalf("a refused operation: %v", err)
	}
	// the same codes on a cookie session keep their plain form
	_, err = l.ClaimCode(context.Background(), sess(), "a", "x", "")
	if err == nil || !strings.Contains(err.Error(), "INVALID_PERSONAL_TOKEN") {
		t.Fatalf("cookie sessions are not renamed: %v", err)
	}
}

// What lafish added after the merge (docs/personal-token-design.md §8): a
// per-minute limit per account, the 站长's switch, and the sender's choice.

func TestPausedAndClaimsOffAreExplained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		switch r.URL.Path {
		case "/api/session":
			w.Write([]byte(`{"error":{"code":"PERSONAL_TOKENS_PAUSED","message":"个人令牌暂时停用了，请在网页上操作。"}}`))
		case "/api/activities/off/page-identity":
			w.Write([]byte(`{"error":{"code":"TOKEN_CLAIMS_OFF","message":"这个红包不让用令牌领"}}`))
		default:
			w.Write([]byte(`{"error":{"code":"PERSONAL_TOKENS_PAUSED","message":"x"}}`))
		}
	}))
	defer srv.Close()
	l := &Lafish{BaseURL: srv.URL + "/api/session", API: srv.URL + "/api", HTTP: httpx.New()}
	ctx := context.Background()

	_, err := l.Whoami(ctx, tokenSess())
	if err == nil || !strings.Contains(err.Error(), "站长暂时关了个人令牌") || !strings.Contains(err.Error(), "令牌本身还有效") {
		t.Fatalf("the switch is off: %v", err)
	}
	_, err = l.PageIdentity(ctx, tokenSess(), "off", "nonce12345")
	if err == nil || !strings.Contains(err.Error(), "发放者不允许用个人令牌领取") || !strings.Contains(err.Error(), "在网页上领") {
		t.Fatalf("the sender turned token claims off: %v", err)
	}
}

func TestATokenOverTheLimitIsToldHowLongToWaitAndNotRetried(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"令牌的请求太频繁了，一分钟后再试。"}}`))
	}))
	defer srv.Close()
	l := &Lafish{API: srv.URL + "/api", HTTP: httpx.New()}
	_, err := l.ClaimCode(context.Background(), tokenSess(), "a", "x", "")
	var rl *httpx.RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfter != 60*time.Second {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(err.Error(), "请求太频繁") || !strings.Contains(err.Error(), "1m0s") {
		t.Errorf("say how long: %v", err)
	}
	if n != 1 {
		t.Errorf("%d requests: a 429 is never retried", n)
	}
}
