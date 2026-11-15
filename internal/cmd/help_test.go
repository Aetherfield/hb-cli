package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func helpOf(t *testing.T, args ...string) string {
	t.Helper()
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append(args, "--help"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

var hasLatinSentence = regexp.MustCompile(`[A-Za-z]{3,} [A-Za-z]{3,} [A-Za-z]{3,}`)

func TestTheTopHelpIsGroupedAndComplete(t *testing.T) {
	h := helpOf(t)
	for _, want := range []string{"用法", "账号", "lafish（hb.lafi.sh）", "Unsnow（hb.unsnow.org）", "其他", "选项", "--json", "--profile",
		"login", "logout", "whoami", "play", "kddb", "assets", "hints", "unsnow", "doctor", "completion", "help"} {
		if !strings.Contains(h, want) {
			t.Errorf("top help lacks %q:\n%s", want, h)
		}
	}
	if ansiRe.MatchString(h) {
		t.Error("a pipe gets plain text")
	}
	if strings.Contains(h, "Available Commands") || strings.Contains(h, "Usage:") || strings.Contains(h, "[flags]") {
		t.Errorf("cobra's English headings are gone:\n%s", h)
	}
}

func TestEveryCommandIsOneLineOfChineseAndEveryOptionDescribed(t *testing.T) {
	root := NewRoot()
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		path := c.CommandPath()
		if c.Hidden {
			return
		}
		if c != root {
			if c.Short == "" || strings.Contains(c.Short, "\n") {
				t.Errorf("%s: Short must be one line, got %q", path, c.Short)
			}
			if n := len([]rune(c.Short)); n > 40 {
				t.Errorf("%s: Short is %d characters, keep it to a line: %q", path, n, c.Short)
			}
			if hasLatinSentence.MatchString(c.Short) {
				t.Errorf("%s: English left in Short: %q", path, c.Short)
			}
		}
		c.Flags().VisitAll(func(f *pflag.Flag) {
			if hasLatinSentence.MatchString(f.Usage) {
				t.Errorf("%s --%s: English left in its description: %q", path, f.Name, f.Usage)
			}
		})
		for _, s := range c.Commands() {
			walk(s)
		}
	}
	walk(root)
}

func TestASubcommandHelpHasItsOwnOptions(t *testing.T) {
	h := helpOf(t, "unsnow", "check")
	for _, want := range []string{"用法", "hb unsnow check <口令|链接|红包编号> [选项]", "--text", "--code", "显示帮助", "全局选项", "--json"} {
		if !strings.Contains(h, want) {
			t.Errorf("lacks %q:\n%s", want, h)
		}
	}
}
