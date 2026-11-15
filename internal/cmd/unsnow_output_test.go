package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"hb/internal/provider/unsnow"
	"hb/internal/ui"
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func ptr(s string) *string { return &s }

func openPacket() unsnow.Packet {
	return unsnow.Packet{
		ID: "cd30e0f6a98e65dadd5b06f17578252d", Type: "equal", Wish: ptr("新年快乐"), TotalAmount: 5,
		Creator: unsnow.Creator{Username: "254"}, Available: true, CanClaim: true, Status: "active",
		Progress: unsnow.Progress{Kind: "shares", Used: 1, Total: 5}, Levels: []int{2, 3, 4},
		Link: "https://hb.unsnow.org/hb/cd30e0f6a98e65dadd5b06f17578252d",
	}
}

func render(u func(*bytes.Buffer) *ui.UI, pk unsnow.Packet) string {
	var b bytes.Buffer
	writePacket(&b, u(&b), pk)
	return b.String()
}

func TestAPacketCardReadsTheSameWithAndWithoutColour(t *testing.T) {
	plain := func(b *bytes.Buffer) *ui.UI { return ui.For(b) }
	colour := func(b *bytes.Buffer) *ui.UI { return ui.WithColor(b) }
	cases := map[string]unsnow.Packet{"open": openPacket()}
	own := openPacket()
	own.CanClaim, own.Reason = false, ptr("这是你发出的红包")
	cases["own"] = own
	done := openPacket()
	done.CanClaim, done.Available, done.Status = false, false, "ended"
	cases["ended"] = done
	locked := openPacket()
	locked.CanClaim, locked.TextCodeLocked, locked.TextCodeLockedUntil = false, true, new(int64)
	cases["locked"] = locked
	for name, pk := range cases {
		p, c := render(plain, pk), render(colour, pk)
		if ansiRe.MatchString(p) {
			t.Errorf("%s: a pipe gets no colour codes:\n%q", name, p)
		}
		if !ansiRe.MatchString(c) {
			t.Errorf("%s: a colour terminal gets colour", name)
		}
		if got := ansiRe.ReplaceAllString(c, ""); got != p {
			t.Errorf("%s: colour changed the text:\nplain  %q\ncolour %q", name, p, got)
		}
	}
	p := render(plain, cases["open"])
	for _, want := range []string{"254 的均分红包", "1/5 份", "总额 5.00 LDC", "祝福 新年快乐", "信任等级限制 2/3/4", "✓ 现在可以领", "https://hb.unsnow.org/hb/cd30e0f6a98e65dadd5b06f17578252d"} {
		if !strings.Contains(p, want) {
			t.Errorf("card lacks %q:\n%s", want, p)
		}
	}
	if o := render(plain, cases["own"]); !strings.Contains(o, "✗ 现在领不了：这是你发出的红包") || strings.Contains(o, "打开下面的链接") {
		t.Errorf("a packet you cannot claim:\n%s", o)
	}
	if e := render(plain, cases["ended"]); !strings.Contains(e, "✗ 现在领不了：已结束") {
		t.Errorf("an ended packet:\n%s", e)
	}
	if l := render(plain, cases["locked"]); !strings.Contains(l, "本轮你已经领过") {
		t.Errorf("a locked text code:\n%s", l)
	}
	t.Logf("\n%s", p)
}

func TestWhoamiShortensAUUIDAndLeavesNumbers(t *testing.T) {
	if got := shortID("f67885b2-636b-46d4-922f-4f38e6ad3aba"); got != "(id f67885b2…)" {
		t.Error(got)
	}
	if got := shortID("468346"); got != "(id 468346)" {
		t.Error(got)
	}
	if shortID("") != "" {
		t.Error("no id, nothing to show")
	}
}
