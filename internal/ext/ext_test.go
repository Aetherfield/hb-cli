package ext

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func serve(t *testing.T, b []byte) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(b) }))
	t.Cleanup(srv.Close)
	return srv.URL + "/x.zip"
}

func sumOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestDownloadChecksTheHashAndUnpacks(t *testing.T) {
	z := zipOf(t, map[string]string{"manifest.json": `{"manifest_version":3}`, "icons/16.png": "png"})
	dir := t.TempDir()
	got, err := Download(context.Background(), nil, serve(t, z), sumOf(z), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(got, "icons", "16.png")); err != nil {
		t.Fatalf("unpacked: %v", err)
	}
	// a wrong hash: nothing is unpacked
	dir2 := t.TempDir()
	if _, err := Download(context.Background(), nil, serve(t, z), strings.Repeat("0", 64), dir2); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("hash mismatch: %v", err)
	}
	if entries, _ := os.ReadDir(dir2); len(entries) != 0 {
		t.Fatal("nothing may be written when the hash is wrong")
	}
}

func TestDownloadRefusesPathsOutsideItsFolder(t *testing.T) {
	for _, bad := range []string{"../evil.js", "a/../../evil.js", "/abs.js", "C:/x.js"} {
		z := zipOf(t, map[string]string{"manifest.json": "{}", bad: "x"})
		dir := t.TempDir()
		if _, err := Download(context.Background(), nil, serve(t, z), sumOf(z), dir); err == nil {
			t.Errorf("%q must be refused", bad)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.js")); err == nil {
			t.Errorf("%q escaped the folder", bad)
		}
	}
}

func TestThePinnedReleaseAddressIsThisVersion(t *testing.T) {
	u := BridgeZipURL()
	if !strings.HasPrefix(u, BridgeRepo+"/releases/download/v"+BridgeVersion+"/") || len(BridgeSHA256) != 64 {
		t.Fatal(u)
	}
}
