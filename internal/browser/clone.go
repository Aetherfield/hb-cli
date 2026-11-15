package browser

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// UserDataDir returns the everyday browser's "User Data" directory for a
// Chromium-family kind, checking that it exists. $HB_USER_DATA overrides it.
func UserDataDir(k Kind) (string, error) {
	d := k.DataDir()
	if d == "" {
		return "", errors.New("cannot work out the user data dir; set HB_USER_DATA")
	}
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s user data dir not found: %s (set HB_USER_DATA)", k.Name, d)
	}
	return d, nil
}

// CloneProfile copies just the files that carry a login (Local State holds
// the cookie-encryption key material; Network/Cookies holds the cookies) from
// the everyday browser's profile into dst, so it can be opened with a
// debugging port. Chrome 136+ ignores that port on the default data dir, which
// is why a copy is needed. The everyday browser must be closed: it locks the
// cookie database. Whether the copied cookies still decrypt depends on the
// browser's cookie-encryption scheme and is not guaranteed.
func CloneProfile(userData, profile, dst string) error {
	src := filepath.Join(userData, profile)
	pairs := []struct{ from, to string }{
		{filepath.Join(userData, "Local State"), filepath.Join(dst, "Local State")},
		{filepath.Join(src, "Network", "Cookies"), filepath.Join(dst, "Default", "Network", "Cookies")},
		{filepath.Join(src, "Network", "Cookies-journal"), filepath.Join(dst, "Default", "Network", "Cookies-journal")},
		{filepath.Join(src, "Preferences"), filepath.Join(dst, "Default", "Preferences")},
	}
	for i, p := range pairs {
		err := copyFile(p.from, p.to)
		if errors.Is(err, os.ErrNotExist) && i >= 2 { // journal/preferences are optional
			continue
		}
		if err != nil {
			if isLocked(err) {
				return fmt.Errorf("cannot read %s: the browser is still running; close ALL its windows (check the tray) and retry", p.from)
			}
			return fmt.Errorf("copy %s: %w", p.from, err)
		}
	}
	return nil
}

func isLocked(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "being used by another process") || strings.Contains(s, "sharing violation")
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
