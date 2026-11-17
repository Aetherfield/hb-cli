package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// ErrNoProfile: the profile asked for is not in this browser.
var ErrNoProfile = errors.New("no such profile")

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
	if _, err := os.Stat(filepath.Join(src, "Network", "Cookies")); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNoProfile, profile)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dst) // never leave a half copy (Local State holds key material) behind
		}
	}()
	for i, p := range pairs {
		err := copyFile(p.from, p.to)
		if errors.Is(err, os.ErrNotExist) && i >= 2 { // journal/preferences are optional
			continue
		}
		if err != nil {
			if isLocked(err) {
				return fmt.Errorf("读不了 %s：浏览器还开着。把它所有窗口都关掉，托盘里也看一下；Edge 还要关掉「启动增强」和「关闭后继续运行后台扩展和应用」（设置 → 系统和性能），或者在任务管理器里结束所有 msedge.exe，再试", p.from)
			}
			return fmt.Errorf("copy %s: %w", p.from, err)
		}
	}
	ok = true
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

// Profile is one profile of a Chromium-family browser: its directory name
// (what --from-profile takes) and the name the browser shows for it.
type Profile struct {
	Dir  string
	Name string
}

// Profiles lists the profiles in a user data dir, from Local State's
// profile.info_cache, keeping only those whose folder exists. The default
// profile comes first, then by directory name.
func Profiles(userData string) ([]Profile, error) {
	b, err := os.ReadFile(filepath.Join(userData, "Local State"))
	if err != nil {
		return nil, err
	}
	var ls struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(b, &ls); err != nil {
		return nil, fmt.Errorf("Local State: %w", err)
	}
	var out []Profile
	for dir, info := range ls.Profile.InfoCache {
		if st, err := os.Stat(filepath.Join(userData, dir)); err != nil || !st.IsDir() {
			continue
		}
		out = append(out, Profile{Dir: dir, Name: info.Name})
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Dir == "Default") != (out[j].Dir == "Default") {
			return out[i].Dir == "Default"
		}
		return out[i].Dir < out[j].Dir
	})
	return out, nil
}
