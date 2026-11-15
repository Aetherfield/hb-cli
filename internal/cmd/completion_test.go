package cmd

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// complete asks cobra what Tab would offer: `hb __complete <words…> <current>`.
func complete(t *testing.T, words ...string) (values []string, directive string) {
	t.Helper()
	root := NewRoot()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"__complete"}, words...))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.HasPrefix(l, ":") {
			directive = l
			continue
		}
		if l != "" {
			values = append(values, strings.SplitN(l, "\t", 2)[0])
		}
	}
	return
}

func has(vs []string, want ...string) bool {
	set := map[string]bool{}
	for _, v := range vs {
		set[v] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func TestTabOffersCommandsSitesAndTheValuesOfOptions(t *testing.T) {
	if v, _ := complete(t, "un"); !has(v, "unsnow") || len(v) != 1 {
		t.Errorf("a prefix of a command: %v", v)
	}
	if v, _ := complete(t, "unsnow", ""); !has(v, "me", "check", "plaza", "sent", "received") {
		t.Errorf("subcommands: %v", v)
	}
	for _, c := range []string{"login", "logout", "whoami"} {
		if v, d := complete(t, c, ""); !has(v, "lafish", "unsnow") || d != ":4" {
			t.Errorf("%s offers the sites and no files: %v %s", c, v, d)
		}
		if v, _ := complete(t, c, "lafish", ""); len(v) != 0 {
			t.Errorf("%s takes one site, then nothing: %v", c, v)
		}
	}
	if v, _ := complete(t, "login", "lafish", "--method", ""); !has(v, "ext", "clone", "paste", "clean", "token") {
		t.Errorf("--method: %v", v)
	}
	if v, _ := complete(t, "play", "--mode", ""); !has(v, "auto", "tui", "cli", "text", "web") {
		t.Errorf("--mode: %v", v)
	}
	for _, c := range []string{"play", "kddb"} {
		if v, _ := complete(t, c, "--render", ""); !has(v, "auto", "glyph", "sprite") {
			t.Errorf("%s --render: %v", c, v)
		}
	}
	if v, _ := complete(t, "login", "--browser", ""); !has(v, "chrome", "edge", "firefox") {
		t.Errorf("--browser: %v", v)
	}
}

// A completion must not help anyone guess: nothing is offered where a link,
// a packet id or a passcode goes.
func TestTabNeverOffersAPasscodeALinkOrAPacketId(t *testing.T) {
	for _, words := range [][]string{{"unsnow", "check", ""}, {"unsnow", "check", "CO"}, {"play", ""}, {"kddb", ""}, {"play", "https://hb.lafi.sh/"}} {
		v, d := complete(t, words...)
		if len(v) != 0 || d != ":4" {
			t.Errorf("%v: offered %v (%s); nothing, and no files", words, v, d)
		}
	}
}

func TestTheNiuScriptSpeaksTheCompdefProtocol(t *testing.T) {
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"completion", "niu"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if s != niuScript {
		t.Fatal("hb completion niu prints the script as it is")
	}
	for _, want := range []string{"NIU_COMP_WORDS", "NIU_COMP_CWORD", "NIU_COMP_RESULT", "hb __complete", `hb:_hb_niu_comp`, "NIU_COMPDEFS"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	// it only computes: a compdef function must not print to the terminal
	if strings.Contains(s, "echo ") || strings.Contains(s, "printf ") {
		t.Error("a compdef function must not print to stdout")
	}
	// and it must be valid shell
	if bash, err := exec.LookPath("bash"); err == nil {
		cmd := exec.Command(bash, "-n")
		cmd.Stdin = strings.NewReader(s)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("bash -n: %v\n%s", err, b)
		}
	}
}
