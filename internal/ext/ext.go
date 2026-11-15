// Package ext is the local pairing server for the MV3 browser extension: the
// extension reads your already-logged-in cookies in your everyday browser and
// posts them to 127.0.0.1, guarded by a one-time pairing code.
package ext

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"hb/internal/auth"
	"hb/internal/provider"
)

//go:embed extension
var files embed.FS

// Extract writes the embedded extension to <dir>/extension and returns that path.
func Extract(dir string) (string, error) {
	dst := filepath.Join(dir, "extension")
	err := fs.WalkDir(files, "extension", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	return dst, err
}

const (
	FirstPort   = 47321
	PortCount   = 10
	maxBadCodes = 8
	maxBody     = 64 << 10
)

type specJSON struct {
	Provider    string   `json:"provider"`
	CookieURLs  []string `json:"cookie_urls"`
	CookieNames []string `json:"cookie_names"`
	CSRFURL     string   `json:"csrf_url,omitempty"`
	CSRFField   string   `json:"csrf_field,omitempty"`
}

type sessionBody struct {
	CSRF      string        `json:"csrf"`
	UserAgent string        `json:"user_agent"`
	Cookies   []auth.Cookie `json:"cookies"`
}

// Pairing is one waiting login: a provider, a one-time code, and a result.
type Pairing struct {
	p      provider.Provider
	spec   provider.LoginSpec
	code   string
	host   string // expected Host header, e.g. 127.0.0.1:47321
	result chan auth.Session

	mu  sync.Mutex
	bad int
}

func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// NewPairing prepares a pairing for provider p expecting requests on host.
func NewPairing(p provider.Provider, host string) (*Pairing, error) {
	code, err := newCode()
	if err != nil {
		return nil, err
	}
	return &Pairing{p: p, spec: p.LoginSpec(), code: code, host: host, result: make(chan auth.Session, 1)}, nil
}

func (pr *Pairing) Code() string { return pr.code }

func (pr *Pairing) codeOK(got string) bool {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if pr.bad >= maxBadCodes {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(pr.code)) == 1 {
		return true
	}
	pr.bad++
	return false
}

func extensionOrigin(o string) bool {
	return strings.HasPrefix(o, "chrome-extension://") || strings.HasPrefix(o, "moz-extension://")
}

// Handler serves the pairing endpoints.
func (pr *Pairing) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]int{"hb": 1})
	})
	mux.HandleFunc("/spec", func(w http.ResponseWriter, r *http.Request) {
		if !pr.codeOK(r.URL.Query().Get("code")) {
			http.Error(w, "bad code", http.StatusForbidden)
			return
		}
		writeJSON(w, http.StatusOK, specJSON{
			Provider: pr.p.Name(), CookieURLs: pr.spec.CookieURLs, CookieNames: pr.spec.CookieNames,
			CSRFURL: pr.spec.CSRFURL, CSRFField: pr.spec.CSRFField,
		})
	})
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if !pr.codeOK(r.URL.Query().Get("code")) {
			http.Error(w, "bad code", http.StatusForbidden)
			return
		}
		var body sessionBody
		if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&body); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		cookies, err := pr.filter(body.Cookies)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		select {
		case pr.result <- auth.Session{Provider: pr.p.Name(), Cookies: cookies, CSRF: body.CSRF, UserAgent: limit(body.UserAgent, 512)}:
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		default:
			http.Error(w, "already received", http.StatusConflict)
		}
	})
	return pr.guard(mux)
}

// guard rejects requests that are not from the local extension: wrong Host
// (DNS rebinding) or a web-page Origin. It also answers CORS preflights.
func (pr *Pairing) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != pr.host {
			http.Error(w, "bad host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			if !extensionOrigin(o) {
				http.Error(w, "bad origin", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// filter keeps only cookies for this provider (name allowlist + domain match).
func (pr *Pairing) filter(in []auth.Cookie) ([]auth.Cookie, error) {
	hosts := map[string]bool{}
	for _, u := range pr.spec.CookieURLs {
		if pu, err := url.Parse(u); err == nil {
			hosts[pu.Hostname()] = true
		}
	}
	names := map[string]bool{}
	for _, n := range pr.spec.CookieNames {
		names[n] = true
	}
	var out []auth.Cookie
	for _, c := range in {
		okHost := false
		for h := range hosts {
			if c.AppliesTo(h) {
				okHost = true
			}
		}
		if !okHost || c.Name == "" || c.Value == "" || (len(names) > 0 && !names[c.Name]) {
			continue
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("no usable cookies for this provider")
	}
	return out, nil
}

// Wait blocks for the extension's session, the context, or a lockout.
func (pr *Pairing) Wait(ctx context.Context) (auth.Session, error) {
	for {
		select {
		case s := <-pr.result:
			return s, nil
		case <-ctx.Done():
			return auth.Session{}, ctx.Err()
		case <-time.After(time.Second):
			pr.mu.Lock()
			locked := pr.bad >= maxBadCodes
			pr.mu.Unlock()
			if locked {
				return auth.Session{}, errors.New("too many wrong pairing codes; run login again")
			}
		}
	}
}

// Listen binds the first free port in the extension's probe range, on
// 127.0.0.1 only, and returns the listener.
func Listen() (net.Listener, error) {
	for i := 0; i < PortCount; i++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", FirstPort+i))
		if err == nil {
			return l, nil
		}
	}
	return nil, fmt.Errorf("ports %d-%d are all busy", FirstPort, FirstPort+PortCount-1)
}

func limit(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
