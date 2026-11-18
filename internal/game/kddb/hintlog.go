package kddb

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A calibration log for the advisor. Before each dig you make yourself, the
// advisor's guess for that cell is noted; when the answer comes back, what the
// cell really was is added. Reading the log later (`hb hints report`) shows
// whether the advisor's "zero risk" ever turned out to be a danger (it must
// not) and whether its odds are about right. It records; it never digs.

// HintRecord is one dig and what the advisor thought of the cell beforehand.
type HintRecord struct {
	At     time.Time `json:"at"`
	Packet string    `json:"packet,omitempty"`
	Card   int       `json:"card"`
	Index  int       `json:"index"`
	Risk   float64   `json:"risk"`   // the advisor's chance of a danger under the cell
	Safe   bool      `json:"safe"`   // it said: no danger possible
	Exact  bool      `json:"exact"`  // all the information could be used
	Kind   Kind      `json:"kind"`   // what the cell turned out to be
	Danger bool      `json:"danger"` // and whether that is a danger

	// From version 2 of the log on (older records have none of these).
	V       int  `json:"v,omitempty"`
	Certain bool `json:"certain,omitempty"` // it said: certainly a danger (the red ✗)
	Known   Kind `json:"known,omitempty"`   // it said: the counts settle that this cell is this kind
}

// HintLog receives records.
type HintLog interface{ Record(HintRecord) }

// FileHintLog appends records to a JSON-lines file; failures are ignored (it is
// only a log).
type FileHintLog struct {
	Path string
	mu   sync.Mutex
}

func (l *FileHintLog) Record(r HintRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(r)
	_, _ = f.Write(append(b, '\n'))
}

// predict is the advisor's guess for a cell about to be dug, or false when the
// cell is not an unknown one (you already know what it is).
func predict(c PublicCard, peeks []Peek, idx int, packet string) (HintRecord, bool) {
	a := Advise(c, peeks)
	r, ok := a.Risk[idx]
	if !ok {
		return HintRecord{}, false
	}
	return HintRecord{At: time.Now().UTC(), Packet: packet, Card: c.ID, Index: idx, Risk: r, Safe: a.IsSafe(idx), Exact: a.Exact,
		V: 2, Certain: a.IsDanger(idx), Known: a.Known[idx]}, true
}

// finish completes a prediction with what the dig turned out to be.
func finish(r HintRecord, res *DigResult) HintRecord {
	r.Kind = res.Kind
	r.Danger = dangerKinds[res.Kind]
	return r
}

// Calibration is what the log says about the advisor.
type Calibration struct {
	Digs         int
	SafeDigs     int // digs the advisor called zero risk
	SafeDangers  int // ... that were a danger anyway (must be 0)
	Bands        []Band
	Inexact      int // digs where part of the information was not used
	Unclassified int

	CertainDigs  int // digs the advisor called certainly a danger (the red ✗)
	CertainWrong int // ... that were not a danger (must be 0)
	KnownDigs    int // digs where the advisor named the cell's kind (log version 2 on)
	KnownWrong   int // ... and named it wrong (must be 0)
	KnownByKind  map[Kind]int
}

// Band is a range of predicted risk and what really happened in it.
type Band struct {
	Low, High float64
	Digs      int
	Dangers   int
	Predicted float64 // the average predicted risk
}

var bandEdges = []float64{0, 0.10, 0.25, 0.50, 1.0001}

// Calibrate reads a log.
func Calibrate(r io.Reader) (Calibration, error) {
	var cal Calibration
	cal.Bands = make([]Band, len(bandEdges)-1)
	for i := range cal.Bands {
		cal.Bands[i].Low, cal.Bands[i].High = bandEdges[i], bandEdges[i+1]
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec HintRecord
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Kind == "" {
			cal.Unclassified++
			continue
		}
		cal.Digs++
		if !rec.Exact {
			cal.Inexact++
		}
		if rec.Known != "" {
			cal.KnownDigs++
			if cal.KnownByKind == nil {
				cal.KnownByKind = map[Kind]int{}
			}
			cal.KnownByKind[rec.Known]++
			if rec.Known != rec.Kind {
				cal.KnownWrong++
			}
		}
		if rec.Certain || (rec.V == 0 && rec.Risk >= 1) { // older logs: a risk of 1 said the same
			cal.CertainDigs++
			if !rec.Danger {
				cal.CertainWrong++
			}
			continue
		}
		if rec.Safe {
			cal.SafeDigs++
			if rec.Danger {
				cal.SafeDangers++
			}
			continue
		}
		for i := range cal.Bands {
			b := &cal.Bands[i]
			if rec.Risk > b.Low && rec.Risk <= b.High || (i == 0 && rec.Risk == 0) {
				b.Digs++
				b.Predicted += rec.Risk
				if rec.Danger {
					b.Dangers++
				}
				break
			}
		}
	}
	for i := range cal.Bands {
		if cal.Bands[i].Digs > 0 {
			cal.Bands[i].Predicted /= float64(cal.Bands[i].Digs)
		}
	}
	sort.Slice(cal.Bands, func(i, j int) bool { return cal.Bands[i].Low < cal.Bands[j].Low })
	return cal, sc.Err()
}

// String writes the report people read.
func (c Calibration) String() string {
	var b strings.Builder
	if c.Digs == 0 {
		return "日志里还没有记录：用 --hints-log 手动挖几格再来看\n"
	}
	fmt.Fprintf(&b, "共 %d 次挖掘（每次都是你自己挖的）\n", c.Digs)
	verdict := "没出过错"
	if c.SafeDangers > 0 {
		verdict = "出错了！建议把这份日志和红包 id 发给我，说明我对规则的理解有地方不对"
	}
	fmt.Fprintf(&b, "预测「零风险」的格子：挖了 %d 次，其中是危险的 %d 次 —— %s\n", c.SafeDigs, c.SafeDangers, verdict)
	if c.CertainDigs > 0 {
		verdict := "没出过错"
		if c.CertainWrong > 0 {
			verdict = "出错了！「一定危险」的格子挖出来不是危险，建议把这份日志和红包 id 发给我"
		}
		fmt.Fprintf(&b, "预测「一定危险」（红色 ✗）的格子：挖了 %d 次，其中不是危险的 %d 次 —— %s\n", c.CertainDigs, c.CertainWrong, verdict)
	}
	if c.KnownDigs > 0 {
		verdict := "没出过错"
		if c.KnownWrong > 0 {
			verdict = "出错了！按数量推定的内容和挖出来的不一样，说明「还剩」的数量假设不成立，建议把这份日志和红包 id 发给我"
		}
		fmt.Fprintf(&b, "按数量推定出内容的格子（深蓝底）：挖了 %d 次，其中推错的 %d 次 —— %s\n", c.KnownDigs, c.KnownWrong, verdict)
	}
	fmt.Fprintf(&b, "其余按预测的危险概率分档，对比实际踩到危险的比例：\n")
	for _, band := range c.Bands {
		if band.Digs == 0 {
			continue
		}
		fmt.Fprintf(&b, "  预测 %.0f%%–%.0f%%：%d 次，预测平均 %.0f%%，实际 %.0f%%（%d 次危险）\n",
			band.Low*100, min(band.High, 1)*100, band.Digs, band.Predicted*100, float64(band.Dangers)/float64(band.Digs)*100, band.Dangers)
	}
	if c.Inexact > 0 {
		fmt.Fprintf(&b, "其中 %d 次的建议没能用上全部信息（仅供参考）\n", c.Inexact)
	}
	b.WriteString("次数少的档位不要当真：几十次以上才有参考意义。\n")
	return b.String()
}
