// Package provider defines the interface every red-packet site implements and
// the registry the commands look providers up in.
package provider

import (
	"context"
	"fmt"
	"sort"

	"hb/internal/auth"
)

// Identity is who a session belongs to.
type Identity struct {
	ID       string `json:"id,omitempty"`
	Username string `json:"username"`
}

// LoginSpec tells the browser flow how to log in and what to harvest.
type LoginSpec struct {
	URL         string   // page to open
	ReadyJS     string   // JS expression (may return a promise): truthy once logged in
	CSRFJS      string   // optional JS expression yielding a CSRF token string
	CookieURLs  []string // URLs whose cookies to harvest
	CookieNames []string // cookie names to keep (all if empty)

	// For the extension and paste flows (no page JS available): a JSON
	// endpoint, fetched with the session cookie, whose CSRFField holds the token.
	CSRFURL   string
	CSRFField string

	// NeedUA: the site is behind Cloudflare, so the session must carry the
	// browser's User-Agent (cf_clearance is bound to it).
	NeedUA bool

	// Token: the site issues its own personal token, and login is pasting it
	// (no browser, no cookie). The other fields are then unused.
	Token *TokenLogin
}

// TokenLogin describes a site's personal token, for `hb login`.
type TokenLogin struct {
	Prefix    string            // what every token starts with, e.g. "hbr_"
	CreateAt  string            // where the person makes one, in words
	Valid     func(string) bool // its exact shape
	OnePerKey bool              // the site allows one per account: making a new one revokes the old
}

// CSRFFetcher is implemented by providers whose mutating routes need a CSRF
// token that can be fetched with the session cookie alone.
type CSRFFetcher interface {
	FetchCSRF(ctx context.Context, s auth.Session) (string, error)
}

type Provider interface {
	Name() string
	LoginSpec() LoginSpec
	Whoami(ctx context.Context, s auth.Session) (Identity, error)
}

var registry = map[string]Provider{}

func Register(p Provider) { registry[p.Name()] = p }

func Get(name string) (Provider, error) {
	p, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q (have: %v)", name, Names())
	}
	return p, nil
}

func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
