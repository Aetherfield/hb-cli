package lafish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"hb/internal/auth"
	"hb/internal/httpx"
)

func sess() auth.Session {
	return auth.Session{Provider: "lafish", CSRF: "csrf-tok", Cookies: []auth.Cookie{{Name: "hb_session", Value: "abc"}}}
}

func TestClaimCodeSendsOriginCSRFAndBody(t *testing.T) {
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/activities/act1/claim" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Origin") != "https://hb.lafi.sh" || r.Header.Get("X-CSRF-Token") != "csrf-tok" {
			t.Errorf("missing origin/csrf: %v", r.Header)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["participationMode"] != "code" || body["input"] != "hello" {
			t.Errorf("body %v", body)
		}
		if id, _ := body["submissionId"].(string); !uuid.MatchString(id) {
			t.Errorf("submissionId %q is not a v4 uuid", id)
		}
		if _, has := body["humanToken"]; has {
			t.Error("humanToken must be omitted when empty")
		}
		w.Write([]byte(`{"result":"judged"}`))
	}))
	defer srv.Close()

	l := &Lafish{API: srv.URL + "/api", HTTP: httpx.New()}
	raw, err := l.ClaimCode(context.Background(), sess(), "act1", "hello", "")
	if err != nil || string(raw) != `{"result":"judged"}` {
		t.Fatalf("raw=%s err=%v", raw, err)
	}
}

func TestErrorsMapToAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/activities/a/claim":
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":{"code":"HUMAN_CHECK_REQUIRED","message":"需要人机验证"},"humanCheck":{"method":"cap"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	l := &Lafish{API: srv.URL + "/api", HTTP: httpx.New()}
	_, err := l.ClaimCode(context.Background(), sess(), "a", "x", "")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 403 || ae.Code != "HUMAN_CHECK_REQUIRED" {
		t.Fatalf("err=%v", err)
	}
	if len(ae.Body) == 0 {
		t.Fatal("body should be kept for the challenge details")
	}
}

func TestRateLimitIsReturnedNotRetried(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	l := &Lafish{API: srv.URL + "/api", HTTP: httpx.New()}
	_, err := l.ClaimCode(context.Background(), sess(), "a", "x", "")
	var rl *httpx.RateLimitedError
	if !errors.As(err, &rl) || n != 1 {
		t.Fatalf("err=%v calls=%d", err, n)
	}
}

func TestGetActivityAndPageURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("X-CSRF-Token") != "" {
			t.Errorf("reads must not carry a CSRF token")
		}
		w.Write([]byte(`{"activity":{"id":"a1","title":"猜数字","status":"active","humanCheck":true,
			"page":{"url":"https://game.example/play/"},"judge":{"judged":3,"unclaimed":"100"}}}`))
	}))
	defer srv.Close()
	l := &Lafish{API: srv.URL + "/api", HTTP: httpx.New()}
	a, err := l.GetActivity(context.Background(), auth.Session{}, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if a.Title != "猜数字" || !a.HumanCheck || a.PageURL() != "https://game.example/play/" || a.Judge["judged"].(float64) != 3 {
		t.Fatalf("%+v", a)
	}
	if (Activity{Page: json.RawMessage(`"https://x.example/"`)}).PageURL() != "https://x.example/" {
		t.Fatal("string page should work")
	}
	if (Activity{}).PageURL() != "" {
		t.Fatal("no page")
	}
}
