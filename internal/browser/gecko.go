package browser

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver

	"hb/internal/auth"
)

// GeckoProfileDir finds the profile directory of a Gecko browser by reading
// its profiles.ini: the install's default profile if there is one, else the
// profile flagged Default=1, else the first. $HB_PROFILE_DIR overrides it.
func GeckoProfileDir(k Kind) (string, error) {
	if d := os.Getenv("HB_PROFILE_DIR"); d != "" {
		return d, nil
	}
	root := k.DataDir()
	f, err := os.Open(filepath.Join(root, "profiles.ini"))
	if err != nil {
		return "", fmt.Errorf("%s: cannot read profiles.ini in %s: %w", k.Name, root, err)
	}
	defer f.Close()
	return parseProfilesINI(f, root)
}

func parseProfilesINI(r io.Reader, root string) (string, error) {
	type section struct {
		name string
		kv   map[string]string
	}
	var secs []*section
	var cur *section
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			cur = &section{name: line[1 : len(line)-1], kv: map[string]string{}}
			secs = append(secs, cur)
		case cur != nil:
			if k, v, ok := strings.Cut(line, "="); ok {
				cur.kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	abs := func(p string, relative bool) string {
		p = filepath.FromSlash(p)
		if relative {
			return filepath.Join(root, p)
		}
		return p
	}
	// 1) an install section's Default (what the browser really launches)
	for _, s := range secs {
		if strings.HasPrefix(s.name, "Install") && s.kv["Default"] != "" {
			return abs(s.kv["Default"], !filepath.IsAbs(filepath.FromSlash(s.kv["Default"]))), nil
		}
	}
	// 2) the profile flagged Default=1, 3) the first profile
	var first string
	for _, s := range secs {
		if !strings.HasPrefix(s.name, "Profile") || s.kv["Path"] == "" {
			continue
		}
		p := abs(s.kv["Path"], s.kv["IsRelative"] != "0")
		if s.kv["Default"] == "1" {
			return p, nil
		}
		if first == "" {
			first = p
		}
	}
	if first != "" {
		return first, nil
	}
	return "", errors.New("no profile found in profiles.ini")
}

// ReadGeckoCookies reads the cookies a login needs straight from the profile's
// cookies.sqlite, which Firefox stores unencrypted. The database is copied
// first (with its WAL), so the browser may stay open and is never touched.
// Only cookies named in names (all if empty) that apply to one of urls are
// returned.
func ReadGeckoCookies(profileDir string, urls, names []string) ([]auth.Cookie, error) {
	src := filepath.Join(profileDir, "cookies.sqlite")
	if _, err := os.Stat(src); err != nil {
		return nil, fmt.Errorf("no cookies.sqlite in %s: %w", profileDir, err)
	}
	tmp, err := os.MkdirTemp("", "hb-gecko-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	dst := filepath.Join(tmp, "cookies.sqlite")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyFile(src+suffix, dst+suffix); err != nil {
			if suffix != "" && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("copy cookies.sqlite%s: %w", suffix, err)
		}
	}
	db, err := sql.Open("sqlite", dst)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name, value, host, path, expiry, isSecure, isHttpOnly FROM moz_cookies`)
	if err != nil {
		return nil, fmt.Errorf("read moz_cookies: %w", err)
	}
	defer rows.Close()

	hosts := make([]string, 0, len(urls))
	for _, u := range urls {
		if pu, err := url.Parse(u); err == nil && pu.Hostname() != "" {
			hosts = append(hosts, pu.Hostname())
		}
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}

	var out []auth.Cookie
	for rows.Next() {
		var c auth.Cookie
		var expiry int64
		var secure, httpOnly int
		if err := rows.Scan(&c.Name, &c.Value, &c.Domain, &c.Path, &expiry, &secure, &httpOnly); err != nil {
			return nil, err
		}
		if len(want) > 0 && !want[c.Name] {
			continue
		}
		applies := false
		for _, h := range hosts {
			if c.AppliesTo(h) {
				applies = true
				break
			}
		}
		if !applies || c.Value == "" {
			continue
		}
		// Old Firefox stored milliseconds, new stores seconds.
		if expiry > 100_000_000_000 {
			expiry /= 1000
		}
		c.Expires = float64(expiry)
		c.Secure, c.HTTPOnly = secure != 0, httpOnly != 0
		out = append(out, c)
	}
	return out, rows.Err()
}
