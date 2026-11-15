package browser

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestKindFromExe(t *testing.T) {
	cases := map[string]string{
		`C:\Program Files\Google\Chrome\Application\chrome.exe`:                      "chrome",
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`:               "edge",
		`C:\Users\u\AppData\Local\BraveSoftware\Brave-Browser\Application\brave.exe`: "brave",
		`C:\Users\u\AppData\Local\Chromium\Application\chrome.exe`:                   "chromium",
		`C:\Program Files\Mozilla Firefox\firefox.exe`:                               "firefox",
		`C:\Program Files\LibreWolf\librewolf.exe`:                                   "librewolf",
		`C:\Program Files\Zen Browser\zen.exe`:                                       "zen",
	}
	for exe, want := range cases {
		k, ok := KindFromExe(exe)
		if !ok || k.ID != want {
			t.Errorf("%s: got %q ok=%v, want %q", exe, k.ID, ok, want)
		}
	}
	if _, ok := KindFromExe(`C:\tools\notepad.exe`); ok {
		t.Error("notepad should not be recognised as a browser")
	}
}

func TestFamilies(t *testing.T) {
	for _, id := range []string{"chrome", "edge", "brave", "vivaldi", "chromium"} {
		if k, _ := KindByID(id); k.Family != Chromium {
			t.Errorf("%s should be Chromium family", id)
		}
	}
	for _, id := range []string{"firefox", "librewolf", "zen"} {
		if k, _ := KindByID(id); k.Family != Gecko {
			t.Errorf("%s should be Gecko family", id)
		}
	}
}

func TestParseProfilesINI(t *testing.T) {
	root := filepath.FromSlash("/data/Firefox")
	const withInstall = `
[Profile0]
Name=default
IsRelative=1
Path=Profiles/old.default

[Profile1]
Name=default-release
IsRelative=1
Path=Profiles/abc.default-release
Default=1

[Install4F96D1932A9F858E]
Default=Profiles/abc.default-release
Locked=1
`
	got, err := parseProfilesINI(strings.NewReader(withInstall), root)
	if err != nil || got != filepath.Join(root, "Profiles", "abc.default-release") {
		t.Fatalf("install default: %q %v", got, err)
	}

	const noInstall = `
[Profile0]
Name=a
IsRelative=1
Path=Profiles/a

[Profile1]
Name=b
IsRelative=1
Path=Profiles/b
Default=1
`
	got, err = parseProfilesINI(strings.NewReader(noInstall), root)
	if err != nil || got != filepath.Join(root, "Profiles", "b") {
		t.Fatalf("Default=1: %q %v", got, err)
	}

	if _, err := parseProfilesINI(strings.NewReader("[General]\nX=1\n"), root); err == nil {
		t.Fatal("expected error when there is no profile")
	}
}

func TestReadGeckoCookies(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "cookies.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE moz_cookies (name TEXT, value TEXT, host TEXT, path TEXT, expiry INTEGER, isSecure INTEGER, isHttpOnly INTEGER)`); err != nil {
		t.Fatal(err)
	}
	rows := [][]any{
		{"hb_session", "good", "hb.lafi.sh", "/", 2000000000, 1, 1},
		{"hb_session", "parent", ".lafi.sh", "/", 2000000000000, 1, 1}, // ms expiry, parent domain
		{"hb_session", "other", "evil.example", "/", 2000000000, 1, 1}, // wrong site
		{"tracking", "x", "hb.lafi.sh", "/", 2000000000, 0, 0},         // not allowlisted
		{"hb_session", "", "hb.lafi.sh", "/", 2000000000, 1, 1},        // empty value
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO moz_cookies VALUES (?,?,?,?,?,?,?)`, r...); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	got, err := ReadGeckoCookies(dir, []string{"https://hb.lafi.sh/"}, []string{"hb_session"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d cookies: %+v", len(got), got)
	}
	vals := map[string]float64{}
	for _, c := range got {
		vals[c.Value] = c.Expires
	}
	if vals["good"] != 2000000000 || vals["parent"] != 2000000000 {
		t.Fatalf("expiry normalisation wrong: %+v", vals)
	}
	// the original file must be untouched / no temp leftovers needed
	if _, err := os.Stat(filepath.Join(dir, "cookies.sqlite")); err != nil {
		t.Fatal(err)
	}
}
