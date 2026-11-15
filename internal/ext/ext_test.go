package ext

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hb/internal/auth"
	"hb/internal/provider"
)

type fakeProv struct{}

func (fakeProv) Name() string { return "fake" }
func (fakeProv) LoginSpec() provider.LoginSpec {
	return provider.LoginSpec{CookieURLs: []string{"https://site.example/"}, CookieNames: []string{"sid"}}
}
func (fakeProv) Whoami(context.Context, auth.Session) (provider.Identity, error) {
	return provider.Identity{}, nil
}

func setup(t *testing.T) (*Pairing, *httptest.Server) {
	t.Helper()
	pr, err := NewPairing(fakeProv{}, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(pr.Handler())
	pr.host = strings.TrimPrefix(srv.URL, "http://")
	t.Cleanup(srv.Close)
	return pr, srv
}

func do(t *testing.T, method, url, origin string, body any) *http.Response {
	t.Helper()
	var r *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, r)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestPairingHappyPath(t *testing.T) {
	pr, srv := setup(t)
	const ext = "chrome-extension://abc"

	if r := do(t, "GET", srv.URL+"/spec?code=000000x", ext, nil); r.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong code accepted: %d", r.StatusCode)
	}
	if r := do(t, "GET", srv.URL+"/spec?code="+pr.Code(), ext, nil); r.StatusCode != 200 {
		t.Fatalf("spec: %d", r.StatusCode)
	}
	body := sessionBody{CSRF: "tok", UserAgent: "UA/1", Cookies: []auth.Cookie{
		{Name: "sid", Value: "v", Domain: "site.example"},
		{Name: "tracking", Value: "x", Domain: "site.example"}, // not allowlisted
		{Name: "sid", Value: "evil", Domain: "other.example"},  // wrong domain
	}}
	if r := do(t, "POST", srv.URL+"/session?code="+pr.Code(), ext, body); r.StatusCode != 200 {
		t.Fatalf("session: %d", r.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s, err := pr.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Cookies) != 1 || s.Cookies[0].Value != "v" || s.CSRF != "tok" || s.UserAgent != "UA/1" {
		t.Fatalf("got %+v", s)
	}
}

func TestRejectsWebPageOriginAndBadHost(t *testing.T) {
	pr, srv := setup(t)
	if r := do(t, "GET", srv.URL+"/spec?code="+pr.Code(), "https://evil.example", nil); r.StatusCode != http.StatusForbidden {
		t.Fatalf("web origin accepted: %d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/hello", nil)
	req.Host = "attacker.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rebinding host accepted: %d", resp.StatusCode)
	}
}

func TestLockoutAfterBadCodes(t *testing.T) {
	pr, srv := setup(t)
	for i := 0; i < maxBadCodes; i++ {
		do(t, "GET", srv.URL+"/spec?code=bad", "chrome-extension://abc", nil)
	}
	// Even the right code is refused once locked.
	if r := do(t, "GET", srv.URL+"/spec?code="+pr.Code(), "chrome-extension://abc", nil); r.StatusCode != http.StatusForbidden {
		t.Fatalf("locked pairing accepted right code: %d", r.StatusCode)
	}
}

func TestExtractWritesManifest(t *testing.T) {
	dir, err := Extract(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.ReadFile("extension/manifest.json"); err != nil {
		t.Fatal(err)
	}
	_ = dir
}
