package kddb

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// recGame is a fake game that serves a fixed state and records what was asked.
type recGame struct {
	*fakeGame
	st      State
	digged  [][2]int
	claimed int
	vouched []string
}

func (g *recGame) State(context.Context) (State, error) { return g.st, nil }
func (g *recGame) Dig(ctx context.Context, card, idx int) (DigAnswer, error) {
	g.digged = append(g.digged, [2]int{card, idx})
	return g.fakeGame.Dig(ctx, card, idx)
}
func (g *recGame) Claim(ctx context.Context) (ClaimAnswer, error) {
	g.claimed++
	return g.fakeGame.Claim(ctx)
}

func shellState() State {
	c := card(1, 0, 0, 5)
	c.Cells[0] = &OpenCell{Kind: "emerald", UID: 7, Name: "me"}
	return State{
		Game: PublicGame{Seq: 1, Left: 1000, Pool: 2000, Rules: Rules{Items: 2, DigInterval: 1},
			Cards: []PublicCard{c, card(2, 5, 0, 3)},
			Feed:  []FeedItem{{Name: "bob", Text: "bob 踩到 TNT"}, {Name: "alice", Text: "alice 挖到钻石"}}}, // newest first
		Me: Me{Chances: 5, Points: 150, Status: "playing", Spyglasses: 1, TableChoices: []string{"h", "x"},
			Items: &Items{Torch: 1, Potion: 1, Ender: 1, Table: 1, Key: 1, Pick: map[string]int{"h": 1}}},
	}
}

func newShell(t *testing.T, g *recGame, stdin string) (*Shell, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	sh := NewShell(context.Background(), g, func(_ context.Context, v string) (string, error) {
		g.vouched = append(g.vouched, v)
		return "红包站已结算", nil
	}, strings.NewReader(stdin), &out)
	return sh, &out
}

func recorder(st State) *recGame {
	return &recGame{fakeGame: &fakeGame{
		digAnswer:   DigAnswer{OK: true, Result: &DigResult{Text: "挖到绿宝石", Amount: 50}, Me: Me{Chances: 4, Points: 200, Status: "playing", CoolingMs: 60000}},
		claimAnswer: ClaimAnswer{OK: true, Voucher: "kd3.v", Points: 150, Me: Me{Status: "claimed"}},
		useAnswer:   UseAnswer{OK: true, Text: "好了", Me: shellState().Me}, // the real answer carries the whole `me`
	}, st: st}
}

func TestStatusIsOneShortLine(t *testing.T) {
	sh, out := newShell(t, recorder(shellState()), "")
	if _, err := sh.Exec("s"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("status is a line plus a line of items, got %d:\n%s", len(lines), out)
	}
	for _, want := range []string{"奖池 10.00/20.00", "待领 1.50", "镐 5"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("status missing %q: %s", want, lines[0])
		}
	}
	if !strings.Contains(lines[1], "sg×1") || !strings.Contains(lines[1], "e×1") || !strings.Contains(lines[1], "tb×1") {
		t.Errorf("items line: %s", lines[1])
	}
}

func TestMapUsesACharacterAndAMarkerPerCellAndExplainsItself(t *testing.T) {
	st := shellState()
	c := card(1, 0, 0, 3)
	c.Cells[0] = &OpenCell{Kind: "emerald", UID: 7} // mine
	c.Cells[1] = &OpenCell{Kind: "tnt", UID: 9}     // someone else's
	c.Fixed = map[string]string{"3": "deco"}
	c.Torches = []Torch{{At: 5, Level: 2, V: 2}}
	st.Game.Cards = []PublicCard{c}
	st.Me.Peeks = []Peek{{CardID: 1, Index: 2, Kind: "diamond"}}
	sh, out := newShell(t, recorder(st), "")
	if _, err := sh.Exec("m"); err != nil {
		t.Fatal(err)
	}
	// each cell is a Chinese character (two columns) and a marker (one); the legend
	// under the map names only what is on it, so the text stands alone when pasted
	want := "#1 普通 3x3 2/9\n  1   2   3\n1 绿* 炸  钻?\n2 饰  .   . 2\n3 .   .   .\n" +
		"绿 绿宝石  钻 钻石  炸 TNT  饰 装饰\n* 你挖的  ? 只有你知道（望远镜/藏宝图）  2 火把 橙\n"
	if out.String() != want {
		t.Fatalf("map:\n%q\nwant:\n%q", out.String(), want)
	}
	// every character stands for one kind only
	seen := map[string]Kind{}
	for _, e := range kindChars {
		if other, dup := seen[e.c]; dup {
			t.Errorf("%q stands for both %s and %s", e.c, other, e.k)
		}
		seen[e.c] = e.k
		if KindNames[e.k] == "" {
			t.Errorf("%s has no name for the legend", e.k)
		}
	}
}

func TestDigConvertsRowAndColumnToACellAndRespectsTheCooldown(t *testing.T) {
	g := recorder(shellState())
	sh, out := newShell(t, g, "")
	if _, err := sh.Exec("d 2 3"); err != nil { // row 2, column 3 of a 5x5 card = cell 7
		t.Fatal(err)
	}
	if len(g.digged) != 1 || g.digged[0] != [2]int{1, 7} {
		t.Fatalf("digged %v, want card 1 cell 7", g.digged)
	}
	if got := strings.TrimSpace(out.String()); got != "挖到绿宝石 +0.50 | 待领 2.00 | 镐 4" {
		t.Fatalf("one short line expected, got %q", got)
	}
	// the cell is now dug from our side: nothing is sent for it again, and the cooldown holds too
	if _, err := sh.Exec("d 3 3"); err == nil || !strings.Contains(err.Error(), "还要等") {
		t.Fatalf("cooldown: %v", err)
	}
	if len(g.digged) != 1 {
		t.Fatal("a command blocked by the cooldown must not reach the server")
	}
	// the single-number form: cell 11 is the same as row 3 column 1
	sh.cool = time.Time{}
	g.digged = nil
	if _, err := sh.Exec("d 11"); err != nil {
		t.Fatal(err)
	}
	if g.digged[0] != [2]int{1, 10} {
		t.Fatalf("d 11 should be cell index 10, got %v", g.digged)
	}
}

func TestDigRefusalsNeverReachTheServer(t *testing.T) {
	for _, c := range []struct {
		name, line string
		edit       func(*State)
		want       string
	}{
		{"already dug", "d 1 1", nil, "已经挖开"},
		{"out of range", "d 9 9", nil, "1..5"},
		{"no position", "d", nil, "位置"},
		{"fixed cell", "d 1 2", func(s *State) { s.Game.Cards[0].Fixed = map[string]string{"1": "deco"} }, "不能挖"},
		{"closed card", "d 1 2", func(s *State) { s.Game.Cards[0].Closed = true }, "关闭"},
		{"already claimed", "d 1 2", func(s *State) { s.Me.Status = "claimed" }, "领取过"},
		{"packet over", "d 1 2", func(s *State) { s.Game.Over = true }, "结束"},
		{"wrap-up", "d 1 2", func(s *State) { past := time.Now().Add(-time.Minute).UnixMilli(); s.Game.WrapUpAt = &past }, "收尾"},
	} {
		st := shellState()
		if c.edit != nil {
			c.edit(&st)
		}
		g := recorder(st)
		sh, _ := newShell(t, g, "")
		_, err := sh.Exec(c.line)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err=%v, want it to contain %q", c.name, err, c.want)
		}
		if len(g.digged) != 0 {
			t.Errorf("%s: the server must not be asked", c.name)
		}
	}
}

func TestUseItems(t *testing.T) {
	g := recorder(shellState())
	sh, _ := newShell(t, g, "")

	if _, err := sh.Exec("u sg 2 2"); err != nil {
		t.Fatal(err)
	}
	if len(g.uses) != 1 || g.uses[0] != (useCall{"spyglass", 1, 6, ""}) {
		t.Fatalf("uses %+v", g.uses)
	}
	if _, err := sh.Exec("u p"); err != nil || g.uses[1].item != "potion" || g.uses[1].card != -1 {
		t.Fatalf("potion: %v %+v", err, g.uses)
	}
	if _, err := sh.Exec("u tb h"); err != nil || g.uses[2] != (useCall{"table", -1, -1, "h"}) {
		t.Fatalf("table: %v %+v", err, g.uses)
	}
	// refusals
	n := len(g.uses)
	for line, want := range map[string]string{
		"u tb v":       "只能选",   // not one of the offered shapes
		"u tb":         "用法",    //
		"u e":          "u e y", // the ender chest destroys half of what you hold: say y
		"u k 1 1":      "宝库",    // a key only works on a vault
		"u zzz":        "不认识",   //
		"u":            "用法",    //
		"u sg 1 1 1 1": "位置",    // too many numbers
		"u ph 9 9":     "1..5",  //
	} {
		sh.cool = time.Time{}
		if _, err := sh.Exec(line); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err=%v, want %q", line, err, want)
		}
	}
	if len(g.uses) != n {
		t.Fatalf("a refused command must not reach the server: %+v", g.uses[n:])
	}
	if _, err := sh.Exec("u e y"); err != nil || g.uses[len(g.uses)-1].item != "ender" {
		t.Fatalf("ender with y: %v", err)
	}
}

func TestYouCannotUseWhatYouDoNotHave(t *testing.T) {
	st := shellState()
	st.Me.Spyglasses = 0
	g := recorder(st)
	sh, _ := newShell(t, g, "")
	if _, err := sh.Exec("u sg 1 2"); err == nil || !strings.Contains(err.Error(), "没有望远镜") {
		t.Fatalf("err=%v", err)
	}
	if len(g.uses) != 0 {
		t.Fatal("not sent")
	}
}

func TestTorchRuleOfTheFirstItemVersion(t *testing.T) {
	st := shellState()
	st.Game.Rules.Items = 1
	g := recorder(st)
	sh, _ := newShell(t, g, "")
	if _, err := sh.Exec("u t 1 1"); err == nil || !strings.Contains(err.Error(), "四格相交") {
		t.Fatalf("err=%v", err)
	}
	if _, err := sh.Exec("u t 2 2"); err != nil || len(g.uses) != 1 {
		t.Fatalf("a corner with a row and column above/left is fine: %v", err)
	}
}

func TestClaimAsksUnlessYouSayY(t *testing.T) {
	// no answer on stdin: nothing is claimed
	g := recorder(shellState())
	sh, _ := newShell(t, g, "")
	if _, err := sh.Exec("claim"); err == nil {
		t.Fatal("claim without confirmation must not go ahead")
	}
	if g.claimed != 0 {
		t.Fatal("not claimed")
	}
	// "n" cancels
	g = recorder(shellState())
	sh, _ = newShell(t, g, "n\n")
	_, _ = sh.Exec("claim")
	if g.claimed != 0 {
		t.Fatal("n cancels")
	}
	// "y" on the next line confirms
	g = recorder(shellState())
	sh, out := newShell(t, g, "y\n")
	if _, err := sh.Exec("claim"); err != nil || g.claimed != 1 || len(g.vouched) != 1 || g.vouched[0] != "kd3.v" {
		t.Fatalf("err=%v claimed=%d vouched=%v", err, g.claimed, g.vouched)
	}
	if !strings.Contains(out.String(), "红包站已结算") {
		t.Fatalf("the result should be shown: %q", out)
	}
	// "claim y" is the one-shot form
	g = recorder(shellState())
	sh, _ = newShell(t, g, "")
	if _, err := sh.Exec("claim y"); err != nil || g.claimed != 1 {
		t.Fatalf("claim y: %v %d", err, g.claimed)
	}
}

func TestFailedSettlementKeepsTheVoucherInTheError(t *testing.T) {
	g := recorder(shellState())
	var out bytes.Buffer
	sh := NewShell(context.Background(), g, func(context.Context, string) (string, error) { return "", errors.New("人机验证") },
		strings.NewReader(""), &out)
	_, err := sh.Exec("claim y")
	if err == nil || !strings.Contains(err.Error(), "kd3.v") {
		t.Fatalf("the voucher must be in the error so it is not lost: %v", err)
	}
}

func TestBossKeyClearsScreenAndScrollback(t *testing.T) {
	sh, out := newShell(t, recorder(shellState()), "")
	if quit, err := sh.Exec("b"); quit || err != nil {
		t.Fatal(quit, err)
	}
	if got := out.String(); !strings.Contains(got, "\x1b[2J") || !strings.Contains(got, "\x1b[3J") {
		t.Fatalf("boss key should clear the screen and the scrollback, got %q", got)
	}
}

func TestThereIsNoRepeatOrLoopCommand(t *testing.T) {
	sh, _ := newShell(t, recorder(shellState()), "")
	for _, c := range []string{"repeat 10 d 1 2", "loop", "auto", "farm", "dig-all", "watch"} {
		if _, err := sh.Exec(c); err == nil || !strings.Contains(err.Error(), "不认识") {
			t.Errorf("%q must not be a command: %v", c, err)
		}
	}
}

func TestRunReadsCommandsUntilQuit(t *testing.T) {
	sh, out := newShell(t, recorder(shellState()), "s\nq\ns\n")
	if err := sh.Run(false); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "奖池") != 1 {
		t.Fatalf("exactly one status before q, got:\n%s", out)
	}
	// errors are printed with "! " and the loop goes on
	sh, out = newShell(t, recorder(shellState()), "nope\ns\n")
	_ = sh.Run(false)
	if !strings.Contains(out.String(), "! 不认识") || !strings.Contains(out.String(), "奖池") {
		t.Fatalf("got:\n%s", out)
	}
	// a prompt only when a person is typing
	sh, out = newShell(t, recorder(shellState()), "q\n")
	_ = sh.Run(true)
	if !strings.Contains(out.String(), "> ") {
		t.Fatal("interactive mode shows a prompt")
	}
}

func TestRunLinesStopsAtTheFirstFailure(t *testing.T) {
	sh, out := newShell(t, recorder(shellState()), "")
	err := sh.RunLines([]string{"d 9 9", "s"})
	if err == nil {
		t.Fatal("a failed command is an error (exit code) for scripts")
	}
	if strings.Contains(out.String(), "奖池") {
		t.Fatal("nothing after a failure may run")
	}
	sh, out = newShell(t, recorder(shellState()), "")
	if err := sh.RunLines([]string{"s", "q", "s"}); err != nil || strings.Count(out.String(), "奖池") != 1 {
		t.Fatalf("q ends the list: %v\n%s", err, out)
	}
}

func TestFeedListsTheOldestFirst(t *testing.T) {
	sh, out := newShell(t, recorder(shellState()), "")
	if _, err := sh.Exec("f"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "alice 挖到钻石\nbob 踩到 TNT\n" {
		t.Fatalf("the newest line should be last (nearest the prompt), got %q", got)
	}
}

func TestListAndSwitchCards(t *testing.T) {
	g := recorder(shellState())
	sh, out := newShell(t, g, "")
	if _, err := sh.Exec("ls"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "*#1 普通 5x5 1/25\n #2 普通 3x3 0/9\n" {
		t.Fatalf("ls:\n%q", got)
	}
	if _, err := sh.Exec("c 2"); err != nil {
		t.Fatal(err)
	}
	if _, err := sh.Exec("c 9"); err == nil {
		t.Fatal("no such card")
	}
	sh.cool = time.Time{}
	if _, err := sh.Exec("d 1 1"); err != nil || g.digged[0] != [2]int{2, 0} {
		t.Fatalf("after c 2, digging uses card 2: %v %v", err, g.digged)
	}
}

func TestSummaryIsShort(t *testing.T) {
	g := recorder(shellState())
	g.summary.Me.Name = "me"
	g.summary.Me.Points = 150
	g.summary.Top = []TopEntry{{UID: 9, Name: "alice", Points: 900}, {UID: 7, Name: "me", Points: 150}}
	sh, out := newShell(t, g, "")
	if _, err := sh.Exec("g"); err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 3 || !strings.Contains(lines[2], "←你") {
		t.Fatalf("summary:\n%s", out)
	}
}
