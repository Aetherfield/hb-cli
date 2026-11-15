package browser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKindFromExeOnMacAndLinux(t *testing.T) {
	cases := map[string]string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome":   "chrome",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge": "edge",
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser":   "brave",
		"/Applications/Firefox.app/Contents/MacOS/firefox":               "firefox",
		"/Applications/Zen.app/Contents/MacOS/zen":                       "zen",
		"/usr/bin/google-chrome-stable":                                  "chrome",
		"/usr/bin/microsoft-edge":                                        "edge",
		"/usr/bin/chromium-browser":                                      "chromium",
		"/snap/bin/chromium":                                             "chromium",
		"/usr/lib/firefox/firefox":                                       "firefox",
		"/opt/microsoft/msedge/msedge":                                   "edge",
		"/usr/bin/librewolf":                                             "librewolf",
	}
	for exe, want := range cases {
		k, ok := KindFromExe(exe)
		if !ok || k.ID != want {
			t.Errorf("%s: got %q ok=%v, want %q", exe, k.ID, ok, want)
		}
	}
	if _, ok := KindFromExe("/usr/bin/vim"); ok {
		t.Error("vim is not a browser")
	}
}

func TestDataDirsPerOS(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"LocalAppData": `C:\Users\u\AppData\Local`, "AppData": `C:\Users\u\AppData\Roaming`}
	getenv := func(k string) string { return env[k] }
	chrome, _ := KindByID("chrome")
	edge, _ := KindByID("edge")
	firefox, _ := KindByID("firefox")

	if got, want := chrome.DataDirFor("darwin", home, getenv), filepath.Join(home, "Library/Application Support/Google/Chrome"); got != want {
		t.Errorf("macOS Chrome: %s, want %s", got, want)
	}
	if got, want := chrome.DataDirFor("linux", home, getenv), filepath.Join(home, ".config/google-chrome"); got != want {
		t.Errorf("Linux Chrome: %s, want %s", got, want)
	}
	if got, want := edge.DataDirFor("linux", home, getenv), filepath.Join(home, ".config/microsoft-edge"); got != want {
		t.Errorf("Linux Edge: %s, want %s", got, want)
	}
	if got, want := firefox.DataDirFor("darwin", home, getenv), filepath.Join(home, "Library/Application Support/Firefox"); got != want {
		t.Errorf("macOS Firefox: %s, want %s", got, want)
	}
	// A Windows path is built from Windows templates, so check it as text (the
	// test may run on Linux or macOS, where filepath uses "/").
	if got := chrome.DataDirFor("windows", home, getenv); got != `C:\Users\u\AppData\Local\Google\Chrome\User Data` {
		t.Errorf("Windows Chrome: %s", got)
	}
	// an unset ${VAR} drops that candidate instead of producing a path like "\Google\..."
	if got := chrome.DataDirFor("windows", home, func(string) string { return "" }); got != "" {
		t.Errorf("no LocalAppData: no candidate, got %q", got)
	}
}

func TestFirefoxDataDirPrefersTheOneWithProfilesIni(t *testing.T) {
	home := t.TempDir()
	plain := filepath.Join(home, ".mozilla/firefox")
	snap := filepath.Join(home, "snap/firefox/common/.mozilla/firefox")
	for _, d := range []string{plain, snap} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// both dirs exist, but only the snap one is the live profile root
	if err := os.WriteFile(filepath.Join(snap, "profiles.ini"), []byte("[General]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ff, _ := KindByID("firefox")
	if got := ff.DataDirFor("linux", home, os.Getenv); got != snap {
		t.Fatalf("got %s, want the one with profiles.ini (%s)", got, snap)
	}
}

func TestEveryKindIsReachableOnEveryOS(t *testing.T) {
	for _, k := range Kinds {
		for _, goos := range []string{"windows", "darwin", "linux"} {
			w := k.whereFor(goos)
			if len(w.data) == 0 {
				t.Errorf("%s has no data dir on %s", k.ID, goos)
			}
			if len(w.exe) == 0 && len(w.name) == 0 {
				t.Errorf("%s cannot be found on %s", k.ID, goos)
			}
		}
		if len(k.ids) == 0 {
			t.Errorf("%s cannot be recognised as the default browser", k.ID)
		}
	}
}

func TestDefaultBrowserAnswers(t *testing.T) {
	cases := map[string]string{
		// Windows ProgId (from `reg query`)
		"    ProgId    REG_SZ    MSEdgeHTM":                   "edge",
		"    ProgId    REG_SZ    ChromeHTML":                  "chrome",
		"    ProgId    REG_SZ    FirefoxURL-308046B0AF4A39CB": "firefox",
		// macOS bundle ids
		"com.google.chrome":     "chrome",
		"com.microsoft.edgemac": "edge",
		"com.brave.browser":     "brave",
		"org.mozilla.firefox":   "firefox",
		// Linux .desktop names
		"google-chrome.desktop\n":    "chrome",
		"chromium-browser.desktop\n": "chromium",
		"microsoft-edge.desktop":     "edge",
		"firefox_firefox.desktop":    "firefox",
		"brave-browser.desktop":      "brave",
		"":                           "",
		"org.gnome.Epiphany.desktop": "",
	}
	for in, want := range cases {
		if got := kindOfDefault(in); got != want {
			t.Errorf("kindOfDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseLaunchServices(t *testing.T) {
	const doc = `{"LSHandlers":[
		{"LSHandlerContentType":"public.html","LSHandlerRoleAll":"com.apple.safari"},
		{"LSHandlerURLScheme":"mailto","LSHandlerRoleAll":"com.apple.mail"},
		{"LSHandlerURLScheme":"https","LSHandlerRoleAll":"com.google.chrome"}]}`
	if got := parseLaunchServices([]byte(doc)); got != "com.google.chrome" {
		t.Fatalf("got %q", got)
	}
	if parseLaunchServices([]byte(`{"LSHandlers":[]}`)) != "" || parseLaunchServices([]byte("not json")) != "" {
		t.Fatal("nothing to find")
	}
	if kindOfDefault(parseLaunchServices([]byte(doc))) != "chrome" {
		t.Fatal("end to end")
	}
}

func TestExpandSkipsUnsetVariables(t *testing.T) {
	get := func(k string) string {
		if k == "ProgramFiles(x86)" {
			return `C:\Program Files (x86)`
		}
		return ""
	}
	if p, ok := expand(`${ProgramFiles(x86)}\X`, "/h", get); !ok || !strings.Contains(p, "Program Files (x86)") {
		t.Fatalf("%q %v", p, ok)
	}
	if _, ok := expand(`${LocalAppData}\X`, "/h", get); ok {
		t.Fatal("an unset variable must be reported")
	}
	if p, ok := expand("~/Library/x", "/Users/me", get); !ok || filepath.ToSlash(p) != "/Users/me/Library/x" {
		t.Fatalf("home expansion: %q", p)
	}
}

func TestResolveAcceptsAPathOnAnyOS(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Google Chrome") // a macOS-style name, no extension
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(exe)
	if err != nil || got.Kind.ID != "chrome" || got.Exe != exe {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := Resolve(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("a missing file is an error")
	}
	if _, err := Resolve("firefoxy-unknown-id"); err == nil {
		t.Fatal("an unknown id is an error")
	}
}
