// Package auth stores provider sessions (cookies + CSRF token) in the OS
// keychain, keyed by profile and provider.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const service = "hb-cli"

var ErrNoSession = errors.New("no saved session; run `hb login <provider>` first")

type Cookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"` // unix seconds, 0 = session cookie
	HTTPOnly bool    `json:"http_only"`
	Secure   bool    `json:"secure"`
}

// AppliesTo reports whether this cookie would be sent to host, by its Domain
// attribute: an exact match, or the cookie domain is a parent of host.
func (c Cookie) AppliesTo(host string) bool {
	d := strings.TrimPrefix(c.Domain, ".")
	return d != "" && (host == d || strings.HasSuffix(host, "."+d))
}

// Session is everything needed to act as the logged-in user on one site.
type Session struct {
	Provider string   `json:"provider"`
	Cookies  []Cookie `json:"cookies"`
	CSRF     string   `json:"csrf,omitempty"`
	// UserAgent is the browser's User-Agent at login. Sites behind Cloudflare
	// bind cf_clearance to it, so requests must send the same one.
	UserAgent string `json:"user_agent,omitempty"`
	// Token is a site's own personal token (Authorization: Bearer), for sites
	// that issue one (Unsnow's CLI API). With a token, no cookie or browser
	// User-Agent is sent.
	Token   string    `json:"token,omitempty"`
	SavedAt time.Time `json:"saved_at"`
}

func key(profile, provider string) string { return profile + ":" + provider }

// Save keeps the session in the OS keychain (Windows Credential Manager, macOS
// Keychain, Linux Secret Service). Where there is no keychain it falls back to
// an owner-only file in hb's config dir; check LastBackend to warn about that.
func Save(profile string, s Session) error {
	s.SavedAt = time.Now().UTC()
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	k := key(profile, s.Provider)
	if kerr := keyring.Set(service, k, string(b)); kerr != nil {
		if ferr := saveFallback(k, s); ferr != nil {
			return fmt.Errorf("neither the keychain (%v) nor the fallback file (%v) could store the session", kerr, ferr)
		}
		setBackend(BackendFile)
		return nil
	}
	deleteFallback(k) // never leave an older copy behind in the file
	setBackend(BackendKeychain)
	return nil
}

// Load reads the session from the keychain, then from the fallback file.
func Load(profile, provider string) (Session, error) {
	k := key(profile, provider)
	v, err := keyring.Get(service, k)
	if err == nil {
		var s Session
		if err := json.Unmarshal([]byte(v), &s); err != nil {
			return Session{}, fmt.Errorf("corrupt session: %w", err)
		}
		return s, nil
	}
	if s, ok := loadFallback(k); ok {
		return s, nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return Session{}, ErrNoSession
	}
	return Session{}, fmt.Errorf("keychain: %w (and no session in the fallback file)", err)
}

// Delete removes the session from both places.
func Delete(profile, provider string) error {
	k := key(profile, provider)
	deleteFallback(k)
	err := keyring.Delete(service, k)
	if err == nil || errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	if _, ok := loadFallback(k); !ok { // the keychain is unusable here; the file copy is already gone
		return nil
	}
	return err
}

// Expired reports whether every persistent cookie has passed its expiry.
// Sessions made only of session cookies are never reported expired here;
// the server decides.
func (s Session) Expired(now time.Time) bool {
	any := false
	for _, c := range s.Cookies {
		if c.Expires <= 0 {
			return false
		}
		any = true
		if float64(now.Unix()) < c.Expires {
			return false
		}
	}
	return any
}

// CookieHeader renders the Cookie request header value.
func (s Session) CookieHeader() string {
	var out string
	for i, c := range s.Cookies {
		if i > 0 {
			out += "; "
		}
		out += c.Name + "=" + c.Value
	}
	return out
}

// Apply sets the session's Cookie header (and the matching User-Agent, when
// one was captured) on req.
func (s Session) Apply(req *http.Request) {
	if s.Token != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
		return
	}
	if h := s.CookieHeader(); h != "" {
		req.Header.Set("Cookie", h)
	}
	if s.UserAgent != "" {
		req.Header.Set("User-Agent", s.UserAgent)
	}
}
