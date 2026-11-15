package ecoji

import (
	"crypto/rand"
	"fmt"
	"strings"
	"testing"
)

// The link a real user was given (percent-encoded, as pasted from a browser).
const realLink = "https://hb.lafi.sh/%F0%9F%A7%A7/%F0%9F%A7%B4%F0%9F%A5%BB%F0%9F%8C%80%F0%9F%A4%B4%F0%9F%94%A8%F0%9F%8D%B1%F0%9F%92%BD%F0%9F%A7%93%F0%9F%92%83%F0%9F%A5%AD%F0%9F%92%B3%F0%9F%91%86%F0%9F%8C%9A%E2%98%95"

const realCode = "🧴🥻🌀🤴🔨🍱💽🧓💃🥭💳👆🌚☕"

func TestAlphabetShape(t *testing.T) {
	if len(alphabet) != 1024 || len(ordinals) != 1024 {
		t.Fatalf("alphabet has %d symbols, %d distinct", len(alphabet), len(ordinals))
	}
}

func TestRealLinkDecodesAndRoundTrips(t *testing.T) {
	id, ok := ParseRef(realLink)
	if !ok {
		t.Fatal("the real link must decode")
	}
	if NormalizeUUID(id) != id {
		t.Fatalf("not a canonical uuid: %q", id)
	}
	back, err := Encode(id)
	if err != nil || back != realCode {
		t.Fatalf("re-encoding gave %q (%v), want %q", back, err, realCode)
	}
	// every way a person may hold the same packet gives the same id
	for _, in := range []string{
		realCode,
		"🧧/" + realCode,
		"https://hb.lafi.sh/🧧/" + realCode,
		"快来领红包 https://hb.lafi.sh/🧧/" + realCode + "，口令见帖",
		strings.ToUpper(id),
		id,
		"  " + id + "\n",
	} {
		got, ok := ParseRef(in)
		if !ok || got != id {
			t.Errorf("ParseRef(%q) = %q, %v; want %q", in, got, ok, id)
		}
	}
}

func TestRoundTripRandomUUIDs(t *testing.T) {
	for i := 0; i < 500; i++ {
		var b [16]byte
		_, _ = rand.Read(b[:])
		id := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
		code, err := Encode(id)
		if err != nil {
			t.Fatal(err)
		}
		if n := len([]rune(code)); n != 14 {
			t.Fatalf("%q has %d symbols", code, n)
		}
		got, ok := Decode(code)
		if !ok || got != id {
			t.Fatalf("%s -> %s -> %s (%v)", id, code, got, ok)
		}
	}
}

func TestRejectsNonCanonicalAndGarbage(t *testing.T) {
	bad := []string{
		"",
		"hello",
		realCode[:len(realCode)-len("☕")], // no padding symbol
		realCode + "🌚",                    // too long
	}
	for _, in := range bad {
		if id, ok := Decode(in); ok {
			t.Errorf("Decode(%q) = %q, should fail", in, id)
		}
	}
	// A code whose spare two bits are not zero is not what Encode produces.
	id, _ := ParseRef(realCode)
	code, _ := Encode(id)
	rs := []rune(code)
	last := rs[12]
	rs[12] = alphabet[(ordinals[last] ^ 1)] // flip the lowest of the two spare bits
	if _, ok := Decode(string(rs)); ok {
		t.Error("non-canonical spare bits must be refused")
	}
	if _, ok := ParseRef("https://hb.lafi.sh/"); ok {
		t.Error("a link without a code is not a packet")
	}
}

func TestVariationSelectorsAreTolerated(t *testing.T) {
	id, _ := ParseRef(realCode)
	withVS := strings.Replace(realCode, "🌀", "🌀️", 1)
	got, ok := Decode(withVS)
	if !ok || got != id {
		t.Fatalf("variation selector after a symbol must be ignored: %q %v", got, ok)
	}
	if _, ok := Decode("️" + realCode); ok {
		t.Error("a selector with nothing before it is invalid")
	}
}
