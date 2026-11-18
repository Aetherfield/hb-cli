package kddb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memLog struct{ recs []HintRecord }

func (l *memLog) Record(r HintRecord) { l.recs = append(l.recs, r) }

func TestCalibrateSeparatesCertainFromEstimated(t *testing.T) {
	log := strings.Join([]string{
		`{"risk":0,"safe":true,"exact":true,"kind":"emerald","danger":false}`,
		`{"risk":0,"safe":true,"exact":true,"kind":"diamond","danger":false}`,
		`{"risk":0.05,"safe":false,"exact":true,"kind":"emerald","danger":false}`,
		`{"risk":0.30,"safe":false,"exact":true,"kind":"tnt","danger":true}`,
		`{"risk":0.30,"safe":false,"exact":false,"kind":"coal","danger":false}`,
		`not json`,
		`{"risk":0.9,"safe":false,"exact":true,"kind":"","danger":false}`, // never completed
		``,
	}, "\n")
	cal, err := Calibrate(strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	if cal.Digs != 5 || cal.SafeDigs != 2 || cal.SafeDangers != 0 || cal.Inexact != 1 || cal.Unclassified != 2 {
		t.Fatalf("%+v", cal)
	}
	var low, mid Band
	for _, b := range cal.Bands {
		if b.Low == 0 {
			low = b
		}
		if b.Low == 0.25 {
			mid = b
		}
	}
	if low.Digs != 1 || low.Dangers != 0 || mid.Digs != 2 || mid.Dangers != 1 || mid.Predicted < 0.29 || mid.Predicted > 0.31 {
		t.Fatalf("bands: low %+v mid %+v", low, mid)
	}
	out := cal.String()
	for _, want := range []string{"共 5 次", "零风险", "没出过错", "预测 25%–50%", "实际 50%"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestAZeroRiskCellThatWasADangerIsReportedLoudly(t *testing.T) {
	cal, _ := Calibrate(strings.NewReader(`{"risk":0,"safe":true,"exact":true,"kind":"tnt","danger":true}`))
	if cal.SafeDangers != 1 {
		t.Fatalf("%+v", cal)
	}
	if out := cal.String(); !strings.Contains(out, "出错了") {
		t.Fatalf("that must not be buried:\n%s", out)
	}
}

func TestAnEmptyLogSaysSo(t *testing.T) {
	cal, _ := Calibrate(strings.NewReader(""))
	if !strings.Contains(cal.String(), "还没有记录") {
		t.Fatal(cal.String())
	}
}

func TestTheTUILogsYourDigsAndWhatTheyWere(t *testing.T) {
	log := &memLog{}
	g := &fakeGame{digAnswer: DigAnswer{OK: true, Result: &DigResult{Kind: "emerald", Text: "挖到绿宝石"}, Me: Me{Status: "playing"}}}
	m := newModel(t, g, nil, Options{Hints: true, HintLog: log, Packet: "pk"}, hintState())
	m.cur = 6 // inside the green torch: the advisor says no danger can be here
	run(t, m, key(m, "enter"))
	if len(log.recs) != 1 {
		t.Fatalf("recs %+v", log.recs)
	}
	r := log.recs[0]
	if !r.Safe || r.Risk != 0 || r.Index != 6 || r.Card != 1 || r.Packet != "pk" || r.Kind != "emerald" || r.Danger {
		t.Fatalf("%+v", r)
	}

	// a cell the advisor was unsure about, and it was a TNT
	m2 := newModel(t, &fakeGame{digAnswer: DigAnswer{OK: true, Result: &DigResult{Kind: "tnt"}, Me: Me{Status: "playing"}}}, nil,
		Options{Hints: true, HintLog: log, Packet: "pk"}, hintState())
	m2.cur = 0
	run(t, m2, key(m2, "enter"))
	r = log.recs[1]
	if r.Safe || r.Risk < 0.24 || r.Risk > 0.26 || !r.Danger {
		t.Fatalf("an estimated cell that was a danger: %+v", r)
	}
	cal, _ := Calibrate(strings.NewReader(`{"risk":0.25,"safe":false,"exact":true,"kind":"tnt","danger":true}`))
	if cal.SafeDangers != 0 {
		t.Fatal("a danger on a merely-estimated cell is not an advisor error")
	}
}

func TestNothingIsLoggedUnlessAskedFor(t *testing.T) {
	log := &memLog{}
	g := &fakeGame{digAnswer: DigAnswer{OK: true, Result: &DigResult{Kind: "emerald"}, Me: Me{Status: "playing"}}}
	m := newModel(t, g, nil, Options{Hints: true}, hintState()) // no HintLog
	m.cur = 6
	run(t, m, key(m, "enter"))
	if len(log.recs) != 0 || m.pending != nil {
		t.Fatal("no log, no records")
	}
}

func TestOnlySuccessfulDigsOfUnknownCellsAreLogged(t *testing.T) {
	log := &memLog{}
	// the server refuses the dig
	g := &fakeGame{digAnswer: DigAnswer{OK: false, Message: "太快了", Me: Me{Status: "playing"}}}
	m := newModel(t, g, nil, Options{Hints: true, HintLog: log}, hintState())
	m.cur = 6
	run(t, m, key(m, "enter"))
	if len(log.recs) != 0 {
		t.Fatalf("a refused dig is not a data point: %+v", log.recs)
	}
	// a cell you already know (you looked at it) has nothing to predict
	st := hintState()
	st.Me.Peeks = []Peek{{CardID: 1, Index: 0, Kind: "emerald"}}
	g2 := &fakeGame{digAnswer: DigAnswer{OK: true, Result: &DigResult{Kind: "emerald"}, Me: Me{Status: "playing"}}}
	m2 := newModel(t, g2, nil, Options{Hints: true, HintLog: log}, st)
	m2.cur = 0
	run(t, m2, key(m2, "enter"))
	if len(log.recs) != 0 {
		t.Fatalf("a peeked cell is known, not predicted: %+v", log.recs)
	}
}

func TestLineModeLogsToo(t *testing.T) {
	log := &memLog{}
	g := recorder(hintState())
	g.fakeGame.digAnswer = DigAnswer{OK: true, Result: &DigResult{Kind: "diamond", Text: "钻石", Amount: 500}, Me: Me{Status: "playing", Chances: 4}}
	sh, _ := newShell(t, g, "")
	sh.Hints, sh.HintLog, sh.Packet = true, log, "pk"
	if _, err := sh.Exec("d 3 3"); err != nil { // the middle cell: under the green torch
		t.Fatal(err)
	}
	if len(log.recs) != 1 || !log.recs[0].Safe || log.recs[0].Index != 12 || log.recs[0].Kind != "diamond" || log.recs[0].Packet != "pk" {
		t.Fatalf("%+v", log.recs)
	}
}

func TestFileLogRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "hints-log.jsonl")
	l := &FileHintLog{Path: path}
	l.Record(HintRecord{Risk: 0, Safe: true, Exact: true, Kind: "emerald"})
	l.Record(HintRecord{Risk: 0.4, Exact: true, Kind: "tnt", Danger: true})
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cal, err := Calibrate(f)
	if err != nil || cal.Digs != 2 || cal.SafeDigs != 1 {
		t.Fatalf("%+v %v", cal, err)
	}
	// an unwritable place is ignored, not fatal: it is only a log
	(&FileHintLog{Path: filepath.Join(path, "x", "y")}).Record(HintRecord{})
}

func TestCalibrateCountsCertainDangersAndDeducedKinds(t *testing.T) {
	log := strings.Join([]string{
		// version 2: certain danger, right; certain danger, wrong; deduced kind right and wrong
		`{"v":2,"risk":1,"certain":true,"exact":true,"kind":"tnt","danger":true,"known":"tnt"}`,
		`{"v":2,"risk":1,"certain":true,"exact":true,"kind":"emerald","danger":false}`,
		`{"v":2,"risk":0,"safe":true,"exact":true,"kind":"coal","danger":false,"known":"coal"}`,
		`{"v":2,"risk":0,"safe":true,"exact":true,"kind":"emerald","danger":false,"known":"coal"}`,
		// an older log: no version, a risk of 1 said "certainly a danger"
		`{"risk":1,"safe":false,"exact":true,"kind":"tnt","danger":true}`,
		// an older log without any of the new fields: nothing is claimed about kinds
		`{"risk":0.3,"safe":false,"exact":true,"kind":"coal","danger":false}`,
	}, "\n")
	cal, err := Calibrate(strings.NewReader(log))
	if err != nil {
		t.Fatal(err)
	}
	if cal.CertainDigs != 3 || cal.CertainWrong != 1 || cal.KnownDigs != 3 || cal.KnownWrong != 1 || cal.KnownByKind["coal"] != 2 {
		t.Fatalf("%+v", cal)
	}
	out := cal.String()
	for _, want := range []string{"一定危险", "不是危险的 1 次", "出错了", "按数量推定出内容", "推错的 1 次"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	// a log with only right answers says so
	good, _ := Calibrate(strings.NewReader(`{"v":2,"risk":1,"certain":true,"exact":true,"kind":"tnt","danger":true,"known":"tnt"}`))
	if g := good.String(); !strings.Contains(g, "没出过错") || strings.Contains(g, "出错了") {
		t.Fatalf("%s", g)
	}
}

func TestTheTUILogsACertainDangerAndADeducedKind(t *testing.T) {
	log := &memLog{}
	st := baseState()
	c := card(1, 0, 0, 3)
	c.Left = map[Kind]int{"tnt": 1}
	for i := range c.Cells {
		if i != 5 {
			c.Cells[i] = &OpenCell{Kind: "emerald", UID: 9}
		}
	}
	st.Game.Cards = []PublicCard{c}
	g := &fakeGame{digAnswer: DigAnswer{OK: true, Result: &DigResult{Kind: "tnt"}, Me: Me{Status: "playing"}}}
	m := newModel(t, g, nil, Options{Hints: true, HintLog: log, Packet: "pk"}, st)
	m.cur = 5
	run(t, m, key(m, "enter"))
	if len(log.recs) != 1 {
		t.Fatalf("%+v", log.recs)
	}
	r := log.recs[0]
	if r.V != 2 || !r.Certain || r.Known != "tnt" || !r.Danger || r.Kind != "tnt" {
		t.Fatalf("the prediction is logged with what it claimed: %+v", r)
	}
}
