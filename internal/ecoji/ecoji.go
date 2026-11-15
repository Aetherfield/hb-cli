// Package ecoji reads and writes lafish's packet links. lafish does not show a
// packet's UUID to people: the link is `https://hb.lafi.sh/🧧/<14 emoji>`, the
// UUID's 128 bits (plus two zero bits) cut into 13 ten-bit Ecoji V2 symbols and
// a trailing ☕ padding symbol. This is a port of lafish's
// packages/contracts/src/ecoji-v2.ts and share-text.ts (same rules: the single
// canonical encoding only).
package ecoji

import (
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strings"
)

const (
	padding           = '☕'
	variationSelector = '️'
	dataSymbols       = 13
)

var (
	alphabet = []rune(alphabetString)
	ordinals = func() map[rune]int {
		m := make(map[rune]int, len(alphabet))
		for i, r := range alphabet {
			m[r] = i
		}
		return m
	}()
	uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	// A packet's address as people paste it: 🧧, an optional slash, then the
	// code. Same stop characters as lafish's share-text.ts.
	addressRE = regexp.MustCompile("🧧/?([^\\s/`'\"<>()\\[\\]，。：:]+)")
)

// NormalizeUUID lowercases a UUID, or returns "" if it is not one.
func NormalizeUUID(id string) string {
	if !uuidRE.MatchString(id) {
		return ""
	}
	return strings.ToLower(id)
}

// Encode returns the 14-symbol code for a UUID.
func Encode(id string) (string, error) {
	u := NormalizeUUID(id)
	if u == "" {
		return "", fmt.Errorf("ecoji: %q is not a UUID", id)
	}
	n, ok := new(big.Int).SetString(strings.ReplaceAll(u, "-", ""), 16)
	if !ok {
		return "", fmt.Errorf("ecoji: bad UUID %q", id)
	}
	n.Lsh(n, 2) // two zero bits complete the last 10-bit symbol
	var b strings.Builder
	mask := big.NewInt(1023)
	for i := dataSymbols - 1; i >= 0; i-- {
		ord := new(big.Int).Rsh(n, uint(i*10))
		ord.And(ord, mask)
		b.WriteRune(alphabet[ord.Int64()])
	}
	b.WriteRune(padding)
	return b.String(), nil
}

// Decode turns a 14-symbol code back into its UUID. It accepts a variation
// selector after a symbol (forums add them) and returns ok=false for anything
// that is not exactly what Encode produces.
func Decode(code string) (string, bool) {
	var symbols []rune
	selectorAllowed := false
	for _, r := range code {
		if r == variationSelector {
			if !selectorAllowed {
				return "", false
			}
			selectorAllowed = false
			continue
		}
		if _, ok := ordinals[r]; !ok && r != padding {
			return "", false
		}
		symbols = append(symbols, r)
		if len(symbols) > dataSymbols+1 {
			return "", false
		}
		selectorAllowed = true
	}
	if len(symbols) != dataSymbols+1 || symbols[dataSymbols] != padding {
		return "", false
	}
	bits := new(big.Int)
	for _, r := range symbols[:dataSymbols] {
		ord, ok := ordinals[r]
		if !ok {
			return "", false
		}
		bits.Lsh(bits, 10)
		bits.Or(bits, big.NewInt(int64(ord)))
	}
	bits.Rsh(bits, 2)
	hex := fmt.Sprintf("%032x", bits)
	if len(hex) != 32 {
		return "", false
	}
	id := hex[0:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:]
	// Upstream ignores the unused bits; only the one canonical encoding counts.
	if back, err := Encode(id); err != nil || back != strings.ReplaceAll(strings.Join(runesToStrings(symbols), ""), string(variationSelector), "") {
		return "", false
	}
	return id, true
}

func runesToStrings(rs []rune) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = string(r)
	}
	return out
}

// ParseRef finds a packet's UUID in whatever a person has: a UUID, the 14-emoji
// code, a link (percent-encoded or not) containing /🧧/<code>, or text that
// mentions one. ok is false when nothing decodes.
func ParseRef(s string) (id string, ok bool) {
	s = strings.TrimSpace(s)
	if u := NormalizeUUID(s); u != "" {
		return u, true
	}
	if u, ok := Decode(s); ok {
		return u, true
	}
	// A pasted link may be percent-encoded; a lone % just means "as it is".
	plain := s
	if d, err := url.PathUnescape(s); err == nil {
		plain = d
	}
	if m := addressRE.FindStringSubmatch(plain); m != nil {
		if u, ok := Decode(m[1]); ok {
			return u, true
		}
	}
	return "", false
}
