package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Family is how we can talk to a browser.
type Family int

const (
	// Chromium browsers (Chrome, Edge, Brave, ...) speak CDP, so hb can launch
	// them with a debugging port and read cookies over the protocol.
	Chromium Family = iota
	// Gecko browsers (Firefox, LibreWolf, Zen) have no CDP; hb reads their
	// plain-text cookies.sqlite instead.
	Gecko
)

// where lists, for one OS, where a browser lives. Paths may start with "~/"
// (home) and use ${VAR} (environment, e.g. ${LocalAppData} on Windows).
type where struct {
	exe  []string // absolute paths of the executable
	name []string // names to look up on PATH
	data []string // Chromium: the "User Data" dir; Gecko: the dir with profiles.ini
}

// Kind describes one supported browser.
type Kind struct {
	ID     string
	Name   string
	Family Family
	win    where
	mac    where
	lin    where
	// ids are lower-case fragments of the system's "default browser" answer:
	// the Windows ProgId, the macOS bundle id, or the Linux .desktop file name.
	ids []string
}

// Kinds lists every supported browser. Order matters for ids: more specific first.
var Kinds = []Kind{
	{ID: "chrome", Name: "Google Chrome", Family: Chromium,
		win: where{
			exe:  []string{`${ProgramFiles}\Google\Chrome\Application\chrome.exe`, `${ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe`, `${LocalAppData}\Google\Chrome\Application\chrome.exe`},
			data: []string{`${LocalAppData}\Google\Chrome\User Data`}},
		mac: where{
			exe:  []string{"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "~/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
			data: []string{"~/Library/Application Support/Google/Chrome"}},
		lin: where{
			exe:  []string{"/opt/google/chrome/chrome"},
			name: []string{"google-chrome", "google-chrome-stable", "chrome"},
			data: []string{"~/.config/google-chrome"}},
		ids: []string{"chromehtml", "com.google.chrome", "google-chrome"}},
	{ID: "edge", Name: "Microsoft Edge", Family: Chromium,
		win: where{
			exe:  []string{`${ProgramFiles(x86)}\Microsoft\Edge\Application\msedge.exe`, `${ProgramFiles}\Microsoft\Edge\Application\msedge.exe`},
			data: []string{`${LocalAppData}\Microsoft\Edge\User Data`}},
		mac: where{
			exe:  []string{"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge", "~/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
			data: []string{"~/Library/Application Support/Microsoft Edge"}},
		lin: where{
			exe:  []string{"/opt/microsoft/msedge/msedge"},
			name: []string{"microsoft-edge", "microsoft-edge-stable", "msedge"},
			data: []string{"~/.config/microsoft-edge"}},
		ids: []string{"msedgehtm", "com.microsoft.edgemac", "microsoft-edge"}},
	{ID: "brave", Name: "Brave", Family: Chromium,
		win: where{
			exe:  []string{`${ProgramFiles}\BraveSoftware\Brave-Browser\Application\brave.exe`, `${LocalAppData}\BraveSoftware\Brave-Browser\Application\brave.exe`},
			data: []string{`${LocalAppData}\BraveSoftware\Brave-Browser\User Data`}},
		mac: where{
			exe:  []string{"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser", "~/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"},
			data: []string{"~/Library/Application Support/BraveSoftware/Brave-Browser"}},
		lin: where{
			name: []string{"brave-browser", "brave", "brave-browser-stable"},
			data: []string{"~/.config/BraveSoftware/Brave-Browser"}},
		ids: []string{"bravehtml", "com.brave.browser", "brave-browser", "brave"}},
	{ID: "vivaldi", Name: "Vivaldi", Family: Chromium,
		win: where{
			exe:  []string{`${LocalAppData}\Vivaldi\Application\vivaldi.exe`, `${ProgramFiles}\Vivaldi\Application\vivaldi.exe`},
			data: []string{`${LocalAppData}\Vivaldi\User Data`}},
		mac: where{
			exe:  []string{"/Applications/Vivaldi.app/Contents/MacOS/Vivaldi", "~/Applications/Vivaldi.app/Contents/MacOS/Vivaldi"},
			data: []string{"~/Library/Application Support/Vivaldi"}},
		lin: where{
			name: []string{"vivaldi", "vivaldi-stable"},
			data: []string{"~/.config/vivaldi"}},
		ids: []string{"vivaldihtm", "com.vivaldi.vivaldi", "vivaldi"}},
	{ID: "chromium", Name: "Chromium", Family: Chromium,
		win: where{
			exe:  []string{`${LocalAppData}\Chromium\Application\chrome.exe`},
			data: []string{`${LocalAppData}\Chromium\User Data`}},
		mac: where{
			exe:  []string{"/Applications/Chromium.app/Contents/MacOS/Chromium", "~/Applications/Chromium.app/Contents/MacOS/Chromium"},
			data: []string{"~/Library/Application Support/Chromium"}},
		lin: where{
			name: []string{"chromium", "chromium-browser"},
			data: []string{"~/.config/chromium", "~/snap/chromium/common/chromium"}},
		ids: []string{"org.chromium.chromium", "chromium"}},
	{ID: "firefox", Name: "Mozilla Firefox", Family: Gecko,
		win: where{
			exe:  []string{`${ProgramFiles}\Mozilla Firefox\firefox.exe`, `${ProgramFiles(x86)}\Mozilla Firefox\firefox.exe`},
			data: []string{`${AppData}\Mozilla\Firefox`}},
		mac: where{
			exe:  []string{"/Applications/Firefox.app/Contents/MacOS/firefox", "~/Applications/Firefox.app/Contents/MacOS/firefox"},
			data: []string{"~/Library/Application Support/Firefox"}},
		lin: where{
			name: []string{"firefox", "firefox-esr"},
			data: []string{"~/.mozilla/firefox", "~/snap/firefox/common/.mozilla/firefox", "~/.var/app/org.mozilla.firefox/.mozilla/firefox"}},
		ids: []string{"firefoxurl", "firefoxhtml", "org.mozilla.firefox", "firefox"}},
	{ID: "librewolf", Name: "LibreWolf", Family: Gecko,
		win: where{
			exe:  []string{`${ProgramFiles}\LibreWolf\librewolf.exe`},
			data: []string{`${AppData}\librewolf`}},
		mac: where{
			exe:  []string{"/Applications/LibreWolf.app/Contents/MacOS/librewolf"},
			data: []string{"~/Library/Application Support/librewolf"}},
		lin: where{
			name: []string{"librewolf"},
			data: []string{"~/.librewolf", "~/.var/app/io.gitlab.librewolf-community/.librewolf"}},
		ids: []string{"librewolf"}},
	{ID: "zen", Name: "Zen Browser", Family: Gecko,
		win: where{
			exe:  []string{`${ProgramFiles}\Zen Browser\zen.exe`},
			data: []string{`${AppData}\zen`}},
		mac: where{
			exe:  []string{"/Applications/Zen.app/Contents/MacOS/zen"},
			data: []string{"~/Library/Application Support/zen"}},
		lin: where{
			name: []string{"zen-browser", "zen"},
			data: []string{"~/.zen", "~/.var/app/app.zen_browser.zen/.zen"}},
		ids: []string{"zen-browser", "app.zen-browser", "zenhtml", "zen.desktop"}},
}

func (k Kind) whereFor(goos string) where {
	switch goos {
	case "windows":
		return k.win
	case "darwin":
		return k.mac
	}
	return k.lin
}

// expand resolves "~/" and ${VAR} in a path; ok is false when a ${VAR} is unset.
func expand(p, home string, getenv func(string) string) (string, bool) {
	if strings.HasPrefix(p, "~/") || p == "~" {
		p = home + p[1:]
	}
	ok := true
	p = os.Expand(p, func(name string) string {
		v := getenv(name)
		if v == "" {
			ok = false
		}
		return v
	})
	return filepath.FromSlash(p), ok
}

func expandAll(ps []string, home string, getenv func(string) string) []string {
	var out []string
	for _, p := range ps {
		if e, ok := expand(p, home, getenv); ok {
			out = append(out, e)
		}
	}
	return out
}

func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

// Installation is an installed browser: its kind and executable.
type Installation struct {
	Kind Kind
	Exe  string
}

// marker is a file that proves a data dir really is that browser's.
func (k Kind) marker() string {
	if k.Family == Gecko {
		return "profiles.ini"
	}
	return "Local State"
}

// DataDirFor is the user-data dir (Chromium) or profiles root (Gecko) on the
// given OS: the first candidate that holds the browser's marker file, else the
// first that exists, else the first candidate.
func (k Kind) DataDirFor(goos, home string, getenv func(string) string) string {
	cands := expandAll(k.whereFor(goos).data, home, getenv)
	for _, d := range cands {
		if _, err := os.Stat(filepath.Join(d, k.marker())); err == nil {
			return d
		}
	}
	for _, d := range cands {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d
		}
	}
	if len(cands) > 0 {
		return cands[0]
	}
	return ""
}

// DataDir is DataDirFor on this machine. $HB_USER_DATA overrides it.
func (k Kind) DataDir() string {
	if d := os.Getenv("HB_USER_DATA"); d != "" {
		return d
	}
	return k.DataDirFor(runtime.GOOS, userHome(), os.Getenv)
}

func (k Kind) find() (string, bool) {
	w := k.whereFor(runtime.GOOS)
	for _, p := range expandAll(w.exe, userHome(), os.Getenv) {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
	}
	for _, n := range w.name {
		if p, err := exec.LookPath(n); err == nil {
			return p, true
		}
	}
	return "", false
}

// Installed lists the supported browsers found on this machine.
func Installed() []Installation {
	var out []Installation
	for _, k := range Kinds {
		if exe, ok := k.find(); ok {
			out = append(out, Installation{Kind: k, Exe: exe})
		}
	}
	return out
}

// KindByID returns the kind with that id.
func KindByID(id string) (Kind, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, k := range Kinds {
		if k.ID == id {
			return k, true
		}
	}
	return Kind{}, false
}

// KindFromExe guesses the kind from an executable path (Windows, macOS or Linux style).
func KindFromExe(exe string) (Kind, bool) {
	low := strings.ToLower(strings.ReplaceAll(exe, `\`, "/"))
	base := low
	if i := strings.LastIndex(low, "/"); i >= 0 {
		base = low[i+1:]
	}
	base = strings.TrimSuffix(base, ".exe")
	has := func(s ...string) bool {
		for _, x := range s {
			if strings.Contains(base, x) {
				return true
			}
		}
		return false
	}
	switch {
	case has("msedge", "microsoft edge", "microsoft-edge"):
		return KindByID("edge")
	case has("brave"):
		return KindByID("brave")
	case has("vivaldi"):
		return KindByID("vivaldi")
	case has("librewolf"):
		return KindByID("librewolf")
	case has("firefox"):
		return KindByID("firefox")
	case base == "zen" || has("zen-browser", "zen browser"):
		return KindByID("zen")
	case strings.Contains(low, "/chromium/") || has("chromium"):
		return KindByID("chromium")
	case has("chrome"):
		return KindByID("chrome")
	}
	return Kind{}, false
}

// kindOfDefault maps the system's default-browser answer to a kind id.
func kindOfDefault(answer string) string {
	low := strings.ToLower(answer)
	if strings.TrimSpace(low) == "" {
		return ""
	}
	for _, k := range Kinds {
		for _, id := range k.ids {
			if strings.Contains(low, id) {
				return k.ID
			}
		}
	}
	return ""
}

// parseLaunchServices reads macOS's LaunchServices handlers (as JSON, from
// `plutil -convert json`) and returns the bundle id that handles https.
func parseLaunchServices(data []byte) string {
	var doc struct {
		Handlers []struct {
			Scheme string `json:"LSHandlerURLScheme"`
			All    string `json:"LSHandlerRoleAll"`
			Viewer string `json:"LSHandlerRoleViewer"`
		} `json:"LSHandlers"`
	}
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	for _, h := range doc.Handlers {
		if strings.EqualFold(h.Scheme, "https") {
			if h.All != "" {
				return h.All
			}
			return h.Viewer
		}
	}
	return ""
}

// defaultAnswer asks the OS which browser opens https links; "" if unknown.
func defaultAnswer() string {
	switch runtime.GOOS {
	case "windows":
		out, err := exec.Command("reg", "query",
			`HKCU\Software\Microsoft\Windows\Shell\Associations\UrlAssociations\https\UserChoice`,
			"/v", "ProgId").Output()
		if err != nil {
			return ""
		}
		return string(out)
	case "darwin":
		plist := filepath.Join(userHome(), "Library/Preferences/com.apple.LaunchServices/com.apple.launchservices.secure.plist")
		out, err := exec.Command("plutil", "-convert", "json", "-o", "-", plist).Output()
		if err != nil {
			return ""
		}
		return parseLaunchServices(out)
	default:
		for _, args := range [][]string{
			{"xdg-settings", "get", "default-web-browser"},
			{"xdg-mime", "query", "default", "x-scheme-handler/https"},
		} {
			if out, err := exec.Command(args[0], args[1:]...).Output(); err == nil && strings.TrimSpace(string(out)) != "" {
				return string(out)
			}
		}
	}
	return ""
}

// DefaultID returns the id of the user's default web browser, or "" if unknown.
func DefaultID() string { return kindOfDefault(defaultAnswer()) }

// Resolve picks a browser from spec: a kind id (chrome, edge, firefox, ...), a
// path to an executable, or "" for the system default (falling back to the
// first installed browser). $HB_BROWSER is used when spec is empty.
func Resolve(spec string) (Installation, error) {
	if spec == "" {
		spec = os.Getenv("HB_BROWSER")
	}
	if spec != "" {
		if k, ok := KindByID(spec); ok {
			exe, found := k.find()
			if !found {
				return Installation{}, fmt.Errorf("%s is not installed (or not found); pass --browser <path to the executable>", k.Name)
			}
			return Installation{Kind: k, Exe: exe}, nil
		}
		if _, err := os.Stat(spec); err != nil {
			return Installation{}, fmt.Errorf("--browser %q is neither a known browser id (%s) nor an existing file", spec, KindIDs())
		}
		k, ok := KindFromExe(spec)
		if !ok {
			return Installation{}, fmt.Errorf("cannot tell what browser %q is; use a known id (%s)", spec, KindIDs())
		}
		return Installation{Kind: k, Exe: spec}, nil
	}
	inst := Installed()
	if len(inst) == 0 {
		return Installation{}, errors.New("no supported browser found; install one or pass --browser <path>")
	}
	if def := DefaultID(); def != "" {
		for _, b := range inst {
			if b.Kind.ID == def {
				return b, nil
			}
		}
	}
	return inst[0], nil
}

// KindIDs lists supported ids for messages.
func KindIDs() string {
	ids := make([]string, len(Kinds))
	for i, k := range Kinds {
		ids[i] = k.ID
	}
	return strings.Join(ids, "|")
}
