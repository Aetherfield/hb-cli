package browser

import (
	"database/sql"
	"fmt"
	"strings"
)

// CookieCheck is what a copied Chromium cookie database says about a site's
// login cookies, without reading their values.
type CookieCheck struct {
	Found      int  // login cookies for the site
	AppBound   int  // of those, under app-bound encryption ("v20"): a copy usually cannot decrypt them
	Persistent int  // of those, kept across a restart (a session cookie is gone once the browser closes)
	Readable   bool // the database could be read at all
}

// InspectCookies looks into a Chromium "Cookies" database (a copy) for the
// cookies named names on hosts. Only metadata is read: the host, the name, the
// first three bytes of the encrypted value (its scheme: v10, v11, v20) and
// whether it persists.
func InspectCookies(dbPath string, hosts, names []string) (CookieCheck, error) {
	var c CookieCheck
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return c, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT host_key, name, substr(encrypted_value, 1, 3), is_persistent FROM cookies`)
	if err != nil {
		return c, fmt.Errorf("read cookies: %w", err)
	}
	defer rows.Close()
	c.Readable = true
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	for rows.Next() {
		var host, name string
		var prefix []byte
		var persistent int
		if err := rows.Scan(&host, &name, &prefix, &persistent); err != nil {
			return c, err
		}
		if len(want) > 0 && !want[name] {
			continue
		}
		h := strings.TrimPrefix(host, ".")
		ok := false
		for _, x := range hosts {
			if h == x || strings.HasSuffix(x, "."+h) {
				ok = true
			}
		}
		if !ok {
			continue
		}
		c.Found++
		if string(prefix) == "v20" {
			c.AppBound++
		}
		if persistent == 1 {
			c.Persistent++
		}
	}
	return c, rows.Err()
}
