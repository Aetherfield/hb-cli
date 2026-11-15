package kddb

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"testing"
	"time"
)

// ---- a reference implementation to check against: brute force ----

// levelV2 is the game's torchLevel2: dangers in the 3x3, opened cells included.
func levelV2(n int) int {
	switch {
	case n == 0:
		return 0
	case n <= 2:
		return 1
	case n <= 4:
		return 2
	}
	return 3
}

// levelV1 is the game's torchLevel: dangers among the corner's cells not opened yet.
func levelV1(n int) int {
	switch {
	case n == 0:
		return 0
	case n <= 2:
		return 1
	}
	return 2
}

type world struct {
	size    int
	danger  []bool // the truth: is there a danger under each cell
	dug     []bool
	fixed   []bool
	peeked  []bool
	torches []Torch
}

func (w *world) card() (PublicCard, []Peek) {
	n := w.size * w.size
	c := PublicCard{ID: 1, Size: w.size, Cells: make([]*OpenCell, n), Left: map[Kind]int{"emerald": 1}, Torches: w.torches}
	var peeks []Peek
	left := 0
	for i := 0; i < n; i++ {
		kind := Kind("emerald")
		if w.danger[i] {
			kind = "tnt"
		}
		switch {
		case w.fixed[i]:
			if c.Fixed == nil {
				c.Fixed = map[string]string{}
			}
			c.Fixed[itoa(i)] = "deco"
		case w.dug[i]:
			c.Cells[i] = &OpenCell{Kind: kind, UID: 9}
		default:
			if w.danger[i] {
				left++
			}
			if w.peeked[i] {
				peeks = append(peeks, Peek{CardID: 1, Index: i, Kind: kind})
			}
		}
	}
	c.Left["tnt"] = left
	return c, peeks
}

func itoa(i int) string { return strconv.Itoa(i) }

func itoaPadded(i int) string {
	return string(rune('0'+i/100%10)) + string(rune('0'+i/10%10)) + string(rune('0'+i%10))
}

// consistent says whether a candidate arrangement of the unknown cells explains
// every torch, by the game's own formulas.
func (w *world) consistent(cand []bool) bool {
	for _, t := range w.torches {
		v2 := t.V == 2
		dangers := 0
		for _, j := range torchArea(w.size, t.At, v2) {
			if v2 || !w.dug[j] { // the first rules only count what is not opened
				if cand[j] {
					dangers++
				}
			}
		}
		got := levelV1(dangers)
		if v2 {
			got = levelV2(dangers)
		}
		if got != t.Level {
			return false
		}
	}
	return true
}

// marginals enumerates every arrangement of the dangers over the unknown cells
// that matches the card's counts and torches, and returns each cell's chance.
func (w *world) marginals(t *testing.T) (map[int]float64, int) {
	n := w.size * w.size
	var unknown []int
	known := make([]bool, n) // dangers we can see: opened cells and the cells you peeked
	dangersLeft := 0
	for i := 0; i < n; i++ {
		switch {
		case w.fixed[i]:
		case w.dug[i]:
			known[i] = w.danger[i]
		case w.peeked[i]:
			known[i] = w.danger[i]
			if w.danger[i] {
				dangersLeft++
			}
		default:
			unknown = append(unknown, i)
			if w.danger[i] {
				dangersLeft++
			}
		}
	}
	peekedDangers := 0
	for i := 0; i < n; i++ {
		if w.peeked[i] && w.danger[i] && !w.dug[i] && !w.fixed[i] {
			peekedDangers++
		}
	}
	D := dangersLeft - peekedDangers
	counts := map[int]float64{}
	total := 0
	cand := make([]bool, n)
	var rec func(at, picked int)
	rec = func(at, picked int) {
		if picked == D {
			copy(cand, known)
			for _, i := range unknown {
				cand[i] = false
			}
			// mark chosen ones: rebuilt below
		}
		_ = at
	}
	_ = rec
	// iterate subsets of size D of `unknown`
	idx := make([]int, D)
	var choose func(start, k int)
	choose = func(start, k int) {
		if k == D {
			copy(cand, known)
			for _, p := range idx {
				cand[unknown[p]] = true
			}
			if w.consistent(cand) {
				total++
				for _, p := range idx {
					counts[unknown[p]]++
				}
			}
			return
		}
		for p := start; p < len(unknown); p++ {
			idx[k] = p
			choose(p+1, k+1)
		}
	}
	choose(0, 0)
	m := map[int]float64{}
	for _, i := range unknown {
		if total > 0 {
			m[i] = counts[i] / float64(total)
		}
	}
	return m, total
}

func randomWorld(rng *rand.Rand) *world {
	size := 3 + rng.Intn(3)
	n := size * size
	w := &world{size: size, danger: make([]bool, n), dug: make([]bool, n), fixed: make([]bool, n), peeked: make([]bool, n)}
	for i := 0; i < n; i++ {
		if rng.Intn(12) == 0 {
			w.fixed[i] = true
		}
	}
	var free []int
	for i := 0; i < n; i++ {
		if !w.fixed[i] {
			free = append(free, i)
		}
	}
	rng.Shuffle(len(free), func(a, b int) { free[a], free[b] = free[b], free[a] })
	nd := rng.Intn(min(len(free)/2, 6) + 1)
	for _, i := range free[:nd] {
		w.danger[i] = true
	}
	for _, i := range free {
		switch {
		case rng.Intn(100) < 35:
			w.dug[i] = true
		case rng.Intn(100) < 12:
			w.peeked[i] = true
		}
	}
	for t := rng.Intn(4); t > 0; t-- {
		at := rng.Intn(n)
		v2 := rng.Intn(2) == 0
		area := torchArea(size, at, v2)
		if len(area) == 0 {
			continue
		}
		dangers, undug := 0, 0
		for _, j := range area {
			if w.danger[j] && (v2 || !w.dug[j]) {
				dangers++
			}
			if !w.dug[j] && !w.fixed[j] {
				undug++
			}
		}
		tor := Torch{At: at, V: 0}
		if v2 {
			tor.V, tor.Level = 2, levelV2(dangers)
		} else {
			if undug == 0 { // a first-rules torch goes out when its four cells are all open
				continue
			}
			tor.Level = levelV1(dangers)
		}
		w.torches = append(w.torches, tor)
	}
	return w
}

// ---- tests ----

func TestAdviceAgreesWithBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261114))
	checked, exactChecked := 0, 0
	withTorch, withSafe, withPartial, withForcedDanger := 0, 0, 0, 0
	for iter := 0; iter < 1500; iter++ {
		w := randomWorld(rng)
		c, peeks := w.card()
		a := Advise(c, peeks)
		marg, total := w.marginals(t)
		if total == 0 {
			t.Fatalf("iter %d: the true arrangement must be among the consistent ones", iter)
		}
		checked++
		if len(w.torches) > 0 {
			withTorch++
		}
		for _, i := range a.Safe {
			if !w.peeked[i] { // a certainty the torches or the counts gave, not just something you saw
				withSafe++
				break
			}
		}
		for _, r := range a.Risk {
			if r > 0 && r < 1 {
				withPartial++
				break
			}
		}
		for _, r := range a.Risk {
			if r == 1 {
				withForcedDanger++
				break
			}
		}

		// soundness, always: a cell called safe never has a danger under it
		for _, i := range a.Safe {
			if w.danger[i] {
				t.Fatalf("iter %d: cell %d is called safe but holds a danger\n%+v\n%+v", iter, i, w, a)
			}
		}
		if !a.Exact {
			continue
		}
		exactChecked++
		// exactness: every unknown cell's chance is the brute-force one
		for i, want := range marg {
			if got := a.Risk[i]; math.Abs(got-want) > 1e-9 {
				t.Fatalf("iter %d: cell %d risk %.6f, brute force says %.6f\n%+v\n%+v", iter, i, got, want, w, a)
			}
			if want == 0 && !a.IsSafe(i) {
				t.Fatalf("iter %d: cell %d is certainly safe but was not reported", iter, i)
			}
			if want > 0 && a.IsSafe(i) {
				t.Fatalf("iter %d: cell %d can hold a danger (%.3f) but was called safe", iter, i, want)
			}
		}
	}
	if exactChecked < checked*9/10 {
		t.Errorf("only %d of %d random cards were solved exactly", exactChecked, checked)
	}
	t.Logf("%d random cards checked against brute force, %d solved exactly; %d have torches, %d have a certain safe cell, %d have uncertain cells, %d have a certain danger",
		checked, exactChecked, withTorch, withSafe, withPartial, withForcedDanger)
	if withTorch < checked/3 || withSafe < checked/10 || withPartial < checked/3 {
		t.Errorf("the random cards are too easy to prove anything: torches %d, safe %d, uncertain %d", withTorch, withSafe, withPartial)
	}
}

func card3(unknownDangers int) PublicCard {
	c := card(1, 0, 0, 3)
	c.Left = map[Kind]int{"tnt": unknownDangers, "emerald": 9 - unknownDangers}
	return c
}

func TestNoDangersLeftMeansEverythingIsSafe(t *testing.T) {
	a := Advise(card3(0), nil)
	if !a.Exact || len(a.Safe) != 9 || a.Dangers != 0 {
		t.Fatalf("%+v", a)
	}
}

func TestWithoutTorchesEveryUnknownCellHasTheSameChance(t *testing.T) {
	c := card3(3)
	c.Cells[0] = &OpenCell{Kind: "emerald"} // one is dug: 8 unknown, 3 dangers
	a := Advise(c, nil)
	if a.Unknown != 8 || a.Dangers != 3 {
		t.Fatalf("%+v", a)
	}
	for i := 1; i < 9; i++ {
		if math.Abs(a.Risk[i]-3.0/8) > 1e-9 {
			t.Fatalf("cell %d: %v", i, a.Risk[i])
		}
	}
	if len(a.Safe) != 0 {
		t.Fatal("nothing is certain here")
	}
}

func TestAGreenTorchMakesItsWholeAreaSafe(t *testing.T) {
	c := card(1, 0, 0, 5)
	c.Left = map[Kind]int{"tnt": 4, "emerald": 21}
	c.Torches = []Torch{{At: 12, Level: 0, V: 2}} // the centre of a 5x5: its 3x3 holds no danger
	a := Advise(c, nil)
	for _, i := range []int{6, 7, 8, 11, 12, 13, 16, 17, 18} {
		if !a.IsSafe(i) {
			t.Errorf("cell %d is inside a green torch's 3x3 and must be safe", i)
		}
	}
	// the other 16 cells share the 4 dangers
	if math.Abs(a.Risk[0]-4.0/16) > 1e-9 {
		t.Errorf("outside the torch each cell carries 4/16, got %v", a.Risk[0])
	}
	if !a.Exact {
		t.Error("exact")
	}
}

func TestFirstRulesTorchOnlyCountsUnopenedCells(t *testing.T) {
	c := card(1, 0, 0, 3)
	c.Left = map[Kind]int{"tnt": 1, "emerald": 8}
	// the corner at cell 4 covers cells 0,1,3,4; a green one means none of them is a danger
	c.Torches = []Torch{{At: 4, Level: 0, V: 0}}
	a := Advise(c, nil)
	for _, i := range []int{0, 1, 3, 4} {
		if !a.IsSafe(i) {
			t.Errorf("cell %d is under a green corner torch", i)
		}
	}
	if math.Abs(a.Risk[2]-1.0/5) > 1e-9 {
		t.Errorf("the one danger is somewhere in the other 5 cells: %v", a.Risk[2])
	}
}

func TestWhatYouSawIsKnown(t *testing.T) {
	c := card3(2)
	// you saw cell 4 and it is a TNT: one danger is accounted for, 8 cells hold the other
	a := Advise(c, []Peek{{CardID: 1, Index: 4, Kind: "tnt"}})
	if a.Dangers != 1 || a.Unknown != 8 {
		t.Fatalf("%+v", a)
	}
	if _, in := a.Risk[4]; in {
		t.Error("a cell you saw is not unknown")
	}
	// you saw cell 2 and it is an emerald: that is a known-safe cell
	b := Advise(c, []Peek{{CardID: 1, Index: 2, Kind: "emerald"}})
	if !b.IsSafe(2) || b.Unknown != 8 {
		t.Fatalf("%+v", b)
	}
	// peeks of other cards do nothing
	if d := Advise(c, []Peek{{CardID: 9, Index: 2, Kind: "tnt"}}); d.Unknown != 9 || d.Dangers != 2 {
		t.Fatalf("%+v", d)
	}
}

func TestAllTheseMustBeDangers(t *testing.T) {
	c := card(1, 0, 0, 3)
	c.Left = map[Kind]int{"tnt": 4, "emerald": 5}
	// four cells around a corner, red (3-4 dangers): with only 4 dangers around, they may be 3 or 4
	c.Torches = []Torch{{At: 4, Level: 2, V: 0}}
	a := Advise(c, nil)
	for _, i := range []int{0, 1, 3, 4} {
		if a.Risk[i] < 0.7 {
			t.Errorf("cell %d should be very likely a danger: %v", i, a.Risk[i])
		}
	}
	// and with exactly 4 dangers all in the corner, the rest are certainly safe
	c.Torches = []Torch{{At: 4, Level: 2, V: 0}}
	c.Left["tnt"] = 3
	b := Advise(c, nil)
	for _, i := range []int{2, 5, 6, 7, 8} {
		if !b.IsSafe(i) {
			t.Errorf("3 dangers, 3-4 of them in the corner: cell %d is outside it and must be safe", i)
		}
	}
}

func TestOldGamesAndClosedCardsGiveNoAdvice(t *testing.T) {
	c := card(1, 0, 0, 3)
	c.Left = nil
	if a := Advise(c, nil); a.Exact || len(a.Risk) != 0 {
		t.Fatalf("no 'left' data: %+v", a)
	}
	c = card3(1)
	c.Closed = true
	if a := Advise(c, nil); len(a.Risk) != 0 {
		t.Fatalf("a closed card has nothing to dig: %+v", a)
	}
}

func TestInconsistentInformationDoesNotInventSafety(t *testing.T) {
	c := card3(1)
	c.Torches = []Torch{{At: 4, Level: 3, V: 2}} // "5 or more dangers" but only 1 exists
	a := Advise(c, nil)
	if a.Exact {
		t.Error("contradictory information must not be reported as exact")
	}
	if len(a.Safe) != 0 {
		t.Errorf("with contradictions nothing may be called certainly safe: %v", a.Safe)
	}
}

func TestLowestListsTheBestNonZeroOnes(t *testing.T) {
	a := Advice{Risk: map[int]float64{3: 0.3, 1: 0.1, 2: 0.1, 4: 0, 5: 1}}
	got := a.Lowest(2)
	if len(got) != 2 || got[0].Index != 1 || got[1].Index != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestBiggestCardWithManyTorchesIsFast(t *testing.T) {
	c := card(1, 0, 0, 7)
	c.Left = map[Kind]int{"tnt": 9, "emerald": 40}
	c.Torches = []Torch{{At: 8, Level: 1, V: 2}, {At: 24, Level: 2, V: 2}, {At: 40, Level: 1, V: 2}, {At: 30, Level: 0, V: 2}}
	start := time.Now()
	a := Advise(c, nil)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
	var safe []int
	safe = append(safe, a.Safe...)
	sort.Ints(safe)
	t.Logf("7x7, 4 torches: exact=%v, %d cells safe, %v", a.Exact, len(a.Safe), time.Since(start))
}
