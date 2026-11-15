package kddb

import (
	"fmt"
	"strconv"
	"strings"
)

// stepMinor: how many cents one smallest unit is ("0.01" -> 1, "1" -> 100,
// "0.5" -> 50). Ported from types.ts stepMinor.
func stepMinor(step string) int {
	if step == "" {
		step = "0.01"
	}
	whole, frac, _ := strings.Cut(step, ".")
	w, _ := strconv.Atoi(whole)
	for len(frac) < 2 {
		frac += "0"
	}
	f, _ := strconv.Atoi(frac[:2])
	if n := w*100 + f; n > 0 {
		return n
	}
	return 1
}

func decimalsOf(step string) int {
	m := stepMinor(step)
	switch {
	case m%100 == 0:
		return 0
	case m%10 == 0:
		return 1
	}
	return 2
}

// FormatUnits writes points (counted in smallest units) for people, the way
// the game's page does: LDC "10000" -> "100.00"; step "1": 2000 -> "2000".
func FormatUnits(points int, step string) string {
	neg := points < 0
	if neg {
		points = -points
	}
	minor := points * stepMinor(step)
	text := fmt.Sprintf("%d.%02d", minor/100, minor%100)
	if d := decimalsOf(step); d != 2 {
		cut := 2 - d
		if d == 0 {
			cut++
		}
		text = text[:len(text)-cut]
	}
	if neg {
		return "-" + text
	}
	return text
}
