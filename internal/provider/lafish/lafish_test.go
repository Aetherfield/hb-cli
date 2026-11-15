package lafish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hb/internal/auth"
	"hb/internal/httpx"
)

func TestWhoamiSendsCookieAndParsesUser(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "hb_session=abc") {
			http.Error(w, "no cookie", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"user":{"id":42,"username":"alice"},"csrfToken":"t"}`))
	}))
	defer srv.Close()

	l := &Lafish{BaseURL: srv.URL, HTTP: httpx.New()}
	s := auth.Session{Provider: "lafish", Cookies: []auth.Cookie{{Name: "hb_session", Value: "abc"}}}
	id, err := l.Whoami(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if id.Username != "alice" || id.ID != "42" {
		t.Fatalf("got %+v", id)
	}
}

func TestWhoamiLoggedOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"user":null,"csrfToken":"t"}`))
	}))
	defer srv.Close()

	l := &Lafish{BaseURL: srv.URL, HTTP: httpx.New()}
	if _, err := l.Whoami(context.Background(), auth.Session{}); err == nil {
		t.Fatal("expected not-logged-in error")
	}
}
