package ui

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestAPipeGetsPlainText(t *testing.T) {
	u := For(&bytes.Buffer{})
	for _, s := range []string{u.Title("x"), u.Section("x"), u.Dim("x"), u.Good("x"), u.Bad("x"), u.Warn("x"), u.Accent("x"), u.Link("x"), u.Bold("x"), u.Mark(true), u.Mark(false)} {
		if ansi.MatchString(s) {
			t.Errorf("no colour codes may reach a pipe: %q", s)
		}
	}
	if u.Mark(true) != "✓" || u.Mark(false) != "✗" {
		t.Error("marks")
	}
}

func TestColourNeverChangesTheText(t *testing.T) {
	plain, col := For(&bytes.Buffer{}), WithColor(&bytes.Buffer{})
	if !ansi.MatchString(col.Good("ok")) {
		t.Fatal("WithColor must draw colour")
	}
	for name, f := range map[string]func(*UI, string) string{
		"title": (*UI).Title, "section": (*UI).Section, "dim": (*UI).Dim, "good": (*UI).Good,
		"bad": (*UI).Bad, "warn": (*UI).Warn, "accent": (*UI).Accent, "link": (*UI).Link, "bold": (*UI).Bold,
	} {
		in := "红包 hb"
		if got := ansi.ReplaceAllString(f(col, in), ""); got != f(plain, in) || got != in {
			t.Errorf("%s: %q vs %q", name, got, f(plain, in))
		}
		if Width(f(col, in)) != Width(in) {
			t.Errorf("%s: colour changed the width", name)
		}
	}
}

func TestPadCountsChineseAsTwoColumns(t *testing.T) {
	u := WithColor(&bytes.Buffer{})
	a, b := Pad("hb", 8), Pad(u.Good("红包"), 8)
	if Width(a) != 8 || Width(b) != 8 {
		t.Fatalf("%d %d", Width(a), Width(b))
	}
	if strings.HasSuffix(Pad("红包红包红包红包", 4), " ") {
		t.Error("a string wider than the column is left as it is")
	}
}
