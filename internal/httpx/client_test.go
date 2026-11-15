package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetriesIdempotentOn5xx(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New()
	c.BaseDelay = time.Millisecond
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || n.Load() != 3 {
		t.Fatalf("status=%d calls=%d", resp.StatusCode, n.Load())
	}
}

func TestPostIsNeverRetried(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := New()
	c.BaseDelay = time.Millisecond
	req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if n.Load() != 1 {
		t.Fatalf("POST was retried: %d calls", n.Load())
	}
}

func TestRateLimitedCarriesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := New().Do(context.Background(), req)
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.RetryAfter != 7*time.Second {
		t.Fatalf("err=%v", err)
	}
}

func TestRedactHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", "hb_session=abc")
	h.Set("Authorization", "Bearer hbk_secret")
	h.Set("X-CSRF-Token", "tok")
	h.Set("Accept", "application/json")
	got := RedactHeaders(h)
	for _, k := range []string{"Cookie", "Authorization", "X-Csrf-Token"} {
		if got[k] != "[redacted]" {
			t.Errorf("%s not redacted: %q", k, got[k])
		}
	}
	if got["Accept"] != "application/json" {
		t.Errorf("Accept altered: %q", got["Accept"])
	}
}
