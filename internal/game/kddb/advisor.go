package kddb

import (
	"fmt"
	"sort"
)

// An advisor for 矿洞夺宝, in the spirit of a Minesweeper solver: it shows what
// the information you already have implies about where the dangers (TNT, end
// crystal, spawner) can be. It uses only what the game shows every player:
//
//   - the card's "还剩" counts (the card's composition minus every opened cell),
//   - the torches' colours (each says how many dangers are around it),
//   - the cells you saw yourself with a spyglass or a treasure map.
//
// It never digs. It is off unless the player asks (--hints), and it is an
// estimate: the odds assume every arrangement the rules allow is equally
// likely. "Zero risk" is different: it means no arrangement the rules allow
// puts a danger there.

var dangerKinds = map[Kind]bool{"tnt": true, "crystal": true, "spawner": true}

// Advice is what can be said about one card.
type Advice struct {
	// Exact is false when part of the information could not be used (it was
	// inconsistent, or too tangled to enumerate); the numbers are then rougher.
	Exact   bool
	Dangers int             // dangers still hidden among the cells you do not know
	Unknown int             // cells you do not know: not dug, not fixed, not seen by you
	Risk    map[int]float64 // chance a danger is under each unknown cell (1 = certainly)
	Safe    []int           // cells with no danger in any arrangement the rules allow (incl. ones you saw)
	Notes   []string
}

// Pick is one cell with its chance of a danger.
type Pick struct {
	Index int
	Risk  float64
}

// Lowest returns up to n cells with the smallest non-zero risk.
func (a Advice) Lowest(n int) []Pick {
	var ps []Pick
	for i, r := range a.Risk {
		if r > 0 && r < 1 {
			ps = append(ps, Pick{i, r})
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Risk != ps[j].Risk {
			return ps[i].Risk < ps[j].Risk
		}
		return ps[i].Index < ps[j].Index
	})
	if len(ps) > n {
		ps = ps[:n]
	}
	return ps
}

// IsSafe reports whether a cell is known to hold no danger.
func (a Advice) IsSafe(idx int) bool {
	for _, s := range a.Safe {
		if s == idx {
			return true
		}
	}
	return false
}

type constraint struct {
	cells0 []int // the unknown cells it covers
	lo0    int
	hi0    int
	// working copy
	cells []int
	lo    int
	hi    int
	cnt   int
	rem   int
}

const (
	maxEnumCells = 24
	maxNodes     = 4_000_000
)

var binom = func() [50][50]float64 {
	var t [50][50]float64
	for n := 0; n < 50; n++ {
		t[n][0] = 1
		for k := 1; k <= n; k++ {
			t[n][k] = t[n-1][k-1]
			if k <= n-1 {
				t[n][k] += t[n-1][k]
			}
		}
	}
	return t
}()

// torchBucket is the range of dangers a torch colour stands for.
func torchBucket(level int, v2 bool) (lo, hi int) {
	switch level {
	case 0:
		return 0, 0
	case 1:
		return 1, 2
	case 2:
		return 3, 4
	}
	if v2 {
		return 5, 1 << 20 // red: five or more
	}
	return 3, 4 // the first item rules have no fourth colour
}

// torchArea is the cells a torch speaks for. The second item rules light the
// 3x3 around the torch's cell; the first light the four cells around the
// corner whose bottom-right cell is `at` (it needs a row and a column above/left).
func torchArea(size, at int, v2 bool) []int {
	row, col := at/size, at%size
	var out []int
	if v2 {
		for r := row - 1; r <= row+1; r++ {
			for c := col - 1; c <= col+1; c++ {
				if r >= 0 && r < size && c >= 0 && c < size {
					out = append(out, r*size+c)
				}
			}
		}
		return out
	}
	if row < 1 || col < 1 {
		return nil
	}
	return []int{at - size - 1, at - size, at - 1, at}
}

// Advise works out what is certain and what is likely on a card. peeks are the
// cells you saw yourself (they may be on other cards; those are ignored).
func Advise(c PublicCard, peeks []Peek) Advice {
	a := Advice{Exact: true, Risk: map[int]float64{}}
	n := c.Size * c.Size
	switch {
	case n == 0 || len(c.Cells) < n:
		a.Exact = false
		return a
	case c.Closed:
		a.Notes = append(a.Notes, "这张卡关闭了，没有可挖的")
		return a
	case len(c.Left) == 0:
		a.Exact = false
		a.Notes = append(a.Notes, "这张卡没有「还剩」数据（旧局），算不了")
		return a
	}

	const (
		unknown = iota
		dug
		fixed
		seen
	)
	state := make([]int, n)
	kind := make([]Kind, n)
	for i := 0; i < n; i++ {
		switch {
		case c.Cells[i] != nil:
			state[i], kind[i] = dug, c.Cells[i].Kind
		case c.Fixed[fmt.Sprint(i)] != "":
			state[i], kind[i] = fixed, Kind(c.Fixed[fmt.Sprint(i)])
		}
	}
	peekedDangers := 0
	for _, p := range peeks {
		if p.CardID == c.ID && p.Index >= 0 && p.Index < n && state[p.Index] == unknown {
			state[p.Index], kind[p.Index] = seen, p.Kind
			if dangerKinds[p.Kind] {
				peekedDangers++
			} else {
				a.Safe = append(a.Safe, p.Index) // you saw it: it is not a danger
			}
		}
	}

	total := 0
	for k := range dangerKinds {
		if v := c.Left[k]; v > 0 {
			total += v
		}
	}
	D := total - peekedDangers
	var U []int
	for i := 0; i < n; i++ {
		if state[i] == unknown {
			U = append(U, i)
		}
	}
	a.Dangers, a.Unknown = D, len(U)
	if D < 0 || D > len(U) {
		a.Exact = false
		a.Notes = append(a.Notes, "「还剩」和你看到的对不上，没法算")
		return a
	}

	inU := map[int]bool{}
	for _, i := range U {
		inU[i] = true
	}

	// Every lit torch is a range on how many dangers sit in the unknown cells it covers.
	var cons []*constraint
	for _, t := range c.Torches {
		v2 := t.V == 2
		area := torchArea(c.Size, t.At, v2)
		known := 0
		var cells []int
		for _, j := range area {
			switch {
			case inU[j]:
				cells = append(cells, j)
			case dangerKinds[kind[j]] && (state[j] == seen || (v2 && state[j] == dug)):
				known++ // second rules count opened cells too; the first only count unopened ones
			}
		}
		lo, hi := torchBucket(t.Level, v2)
		lo, hi = max(0, lo-known), hi-known
		if hi < 0 || lo > len(cells) {
			a.Exact = false
			a.Notes = append(a.Notes, fmt.Sprintf("火把（第 %d 格）和已知的对不上，没用上", t.At+1))
			continue
		}
		cons = append(cons, &constraint{cells0: cells, lo0: lo, hi0: min(hi, len(cells))})
	}

	// Propagate what is forced: "no dangers here" and "all of these are dangers".
	forcedSafe, forcedDanger := map[int]bool{}, map[int]bool{}
	for changed := true; changed; {
		changed = false
		for _, k := range cons {
			k.cells, k.lo, k.hi = k.cells[:0:0], k.lo0, k.hi0
			for _, i := range k.cells0 {
				switch {
				case forcedSafe[i]:
				case forcedDanger[i]:
					k.lo--
					k.hi--
				default:
					k.cells = append(k.cells, i)
				}
			}
			k.lo = max(0, k.lo)
			k.hi = min(k.hi, len(k.cells))
			if k.hi < 0 || k.lo > len(k.cells) {
				a.Exact = false
				a.Notes = append(a.Notes, "火把之间对不上，其中一个没用上")
				k.cells, k.lo, k.hi = nil, 0, 0
				continue
			}
			switch {
			case len(k.cells) == 0:
			case k.hi == 0:
				for _, i := range k.cells {
					forcedSafe[i] = true
				}
				changed = true
			case k.lo == len(k.cells):
				for _, i := range k.cells {
					forcedDanger[i] = true
				}
				changed = true
			}
		}
	}
	D -= len(forcedDanger)
	var active []int // unknown cells that are neither forced safe nor forced dangerous
	for _, i := range U {
		if !forcedSafe[i] && !forcedDanger[i] {
			active = append(active, i)
		}
	}
	if D < 0 || D > len(active) {
		a.Exact = false
		a.Notes = append(a.Notes, "火把和「还剩」对不上，没法算")
		return a
	}

	// What is left of the constraints, over the cells they still touch.
	var live []*constraint
	pos := map[int]int{}
	var C []int
	for _, k := range cons {
		if len(k.cells) == 0 || (k.lo == 0 && k.hi >= len(k.cells)) {
			continue // says nothing any more
		}
		live = append(live, k)
		for _, i := range k.cells {
			if _, ok := pos[i]; !ok {
				pos[i] = len(C)
				C = append(C, i)
			}
		}
	}
	m := len(C)
	if m > maxEnumCells {
		a.Exact = false
		a.Notes = append(a.Notes, "火把交叠太多，只用了确定的部分")
		live, C, m, pos = nil, nil, 0, map[int]int{}
	}
	F := len(active) - m

	// Enumerate the arrangements of the constrained cells; the rest are spread
	// over the free cells in every possible way (binomially).
	cellCons := make([][]int, m)
	for ci, k := range live {
		k.cnt, k.rem = 0, len(k.cells)
		for _, i := range k.cells {
			cellCons[pos[i]] = append(cellCons[pos[i]], ci)
		}
	}
	assign := make([]bool, m)
	wPos := make([]float64, m)
	var W, wFree float64
	nodes := 0
	aborted := false
	var rec func(p, k int)
	rec = func(p, k int) {
		if aborted {
			return
		}
		if nodes++; nodes > maxNodes {
			aborted = true
			return
		}
		if p == m {
			r := D - k
			if r < 0 || r > F {
				return
			}
			w := binom[F][r]
			W += w
			for q := 0; q < m; q++ {
				if assign[q] {
					wPos[q] += w
				}
			}
			if F > 0 {
				wFree += w * float64(r) / float64(F)
			}
			return
		}
		for v := 0; v <= 1; v++ {
			assign[p] = v == 1
			ok := k+v <= D && (m-p-1)+F >= D-k-v
			for _, ci := range cellCons[p] {
				live[ci].rem--
				if v == 1 {
					live[ci].cnt++
				}
				if live[ci].cnt > live[ci].hi || live[ci].cnt+live[ci].rem < live[ci].lo {
					ok = false
				}
			}
			if ok {
				rec(p+1, k+v)
			}
			for _, ci := range cellCons[p] {
				live[ci].rem++
				if v == 1 {
					live[ci].cnt--
				}
			}
		}
		assign[p] = false
	}
	rec(0, 0)

	uniform := func() {
		a.Exact = false
		for _, i := range active {
			a.Risk[i] = float64(D) / float64(len(active))
		}
		if D == 0 {
			a.Safe = append(a.Safe, active...)
		}
	}
	switch {
	case aborted || W == 0:
		a.Notes = append(a.Notes, "算不出来，按平均估算")
		uniform()
	default:
		for q, i := range C {
			p := wPos[q] / W
			a.Risk[i] = p
			if wPos[q] == 0 {
				a.Safe = append(a.Safe, i)
			}
		}
		if F > 0 {
			free := wFree / W
			inC := map[int]bool{}
			for _, i := range C {
				inC[i] = true
			}
			for _, i := range active {
				if !inC[i] {
					a.Risk[i] = free
					if wFree == 0 {
						a.Safe = append(a.Safe, i)
					}
				}
			}
		}
	}
	for i := range forcedSafe {
		a.Risk[i] = 0
		a.Safe = append(a.Safe, i)
	}
	for i := range forcedDanger {
		a.Risk[i] = 1
	}
	sort.Ints(a.Safe)
	return a
}
