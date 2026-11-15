package cmd

import (
	"bytes"
	"strings"
	"testing"

	"hb/internal/provider"
)

func loginWith(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	root := NewRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"login"}, args...))
	t.Setenv("HB_HOME", t.TempDir())
	err := root.Execute()
	return out.String(), err
}

func TestTokenLoginTakesOnlyTheRightShape(t *testing.T) {
	for _, c := range []struct{ site, token string }{
		{"lafish", "hbr_0123456789abcdefghijklmnopqrstuvwxyzABCDEFG"}, // another site's token
		{"lafish", "hbp_short"},
		{"unsnow", "hbp_0123456789abcdefghijklmnopqrstuv"},
		{"unsnow", "not a token"},
	} {
		_, err := loginWith(t, c.token+"\n", c.site, "--token-stdin")
		if err == nil || !strings.Contains(err.Error(), "这不像 "+c.site+" 的令牌") {
			t.Errorf("%s %q: %v", c.site, c.token, err)
		}
	}
}

func TestTokenLoginNeedsASiteThatHasTokens(t *testing.T) {
	p, err := provider.Get("lafish")
	if err != nil || p.LoginSpec().Token == nil {
		t.Fatal("lafish offers a personal token")
	}
	if u, _ := provider.Get("unsnow"); len(u.LoginSpec().CookieURLs) != 0 || u.LoginSpec().Token == nil {
		t.Fatal("unsnow is token only")
	}
	if _, err := loginWith(t, "x", "lafish", "--method", "nonsense"); err == nil || !strings.Contains(err.Error(), "unknown --method") {
		t.Fatalf("%v", err)
	}
}
