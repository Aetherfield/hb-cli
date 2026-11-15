// Package mcpserver is hb's MCP server: what an AI assistant may do with the
// person's red-packet accounts, and, as important, what it may not.
//
// The rules that matter are in code, not in a prompt. A prompt can be argued
// out of its rules (a red packet's title is text anyone can write); code
// cannot. So claiming is two steps, each claim is confirmed by the person in
// their own client, the pace and the number of claims are capped, a failure
// pauses claiming, and nothing here can guess a passcode.
package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Limits are the caps on claiming. They are chosen by the person who starts
// the server (hb mcp flags), never by the assistant, and cannot go past the
// Ceiling values.
type Limits struct {
	MinGap     time.Duration // between two claim attempts
	PerHour    int
	PerDay     int
	FailPause  time.Duration // after a claim that did not work
	BreakAfter int           // consecutive failures that trip the breaker
	BreakFor   time.Duration // how long the breaker holds
	PreviewTTL time.Duration // how long a preview stays good
}

// The most any flag may ask for.
const (
	CeilingPerHour = 20
	CeilingPerDay  = 60
)

// DefaultLimits are what hb mcp runs with.
func DefaultLimits() Limits {
	return Limits{
		MinGap: 10 * time.Second, PerHour: 5, PerDay: 20,
		FailPause: time.Minute, BreakAfter: 2, BreakFor: 10 * time.Minute,
		PreviewTTL: 5 * time.Minute,
	}
}

// Clamp brings the limits inside what is allowed.
func (l Limits) Clamp() Limits {
	d := DefaultLimits()
	if l.MinGap < d.MinGap {
		l.MinGap = d.MinGap
	}
	if l.PerHour < 0 || l.PerHour > CeilingPerHour {
		l.PerHour = CeilingPerHour
	}
	if l.PerDay < 0 || l.PerDay > CeilingPerDay {
		l.PerDay = CeilingPerDay
	}
	if l.FailPause < d.FailPause {
		l.FailPause = d.FailPause
	}
	if l.BreakAfter < 1 {
		l.BreakAfter = d.BreakAfter
	}
	if l.BreakFor < d.BreakFor {
		l.BreakFor = d.BreakFor
	}
	if l.PreviewTTL <= 0 || l.PreviewTTL > d.PreviewTTL {
		l.PreviewTTL = d.PreviewTTL
	}
	return l
}

// Refusal says why a claim may not go ahead now. It is not an error of the
// site: nothing was sent.
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return r.Reason }

// Guard keeps the claim count and pace. Its state is a file, so a restart of
// the server does not give the assistant a fresh allowance.
type Guard struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
	lim  Limits
	st   guardState
}

type guardState struct {
	Attempts     []int64 `json:"attempts"` // unix ms of every claim attempt
	Consecutive  int     `json:"consecutive_failures"`
	BlockedUntil int64   `json:"blocked_until"` // unix ms
	Reason       string  `json:"blocked_reason,omitempty"`
}

// NewGuard loads the state kept at path ("" keeps it in memory only).
func NewGuard(path string, lim Limits, now func() time.Time) *Guard {
	if now == nil {
		now = time.Now
	}
	g := &Guard{path: path, now: now, lim: lim.Clamp()}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &g.st)
		}
	}
	g.trim()
	return g
}

func (g *Guard) trim() {
	cut := g.now().Add(-24 * time.Hour).UnixMilli()
	keep := g.st.Attempts[:0]
	for _, a := range g.st.Attempts {
		if a > cut {
			keep = append(keep, a)
		}
	}
	g.st.Attempts = keep
}

func (g *Guard) save() {
	if g.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(g.path), 0o700); err != nil {
		return
	}
	b, _ := json.Marshal(g.st)
	tmp := g.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, g.path)
	}
}

func wait(d time.Duration) string {
	if d < time.Second {
		d = time.Second
	}
	if d >= time.Minute {
		return fmt.Sprintf("%d 分钟", int((d+time.Minute-1)/time.Minute))
	}
	return fmt.Sprintf("%d 秒", int((d+time.Second-1)/time.Second))
}

// Check says whether a claim may be attempted now.
func (g *Guard) Check() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.trim()
	now := g.now()
	if until := time.UnixMilli(g.st.BlockedUntil); until.After(now) {
		return &Refusal{fmt.Sprintf("领取暂停中（%s），还要等 %s", g.st.Reason, wait(until.Sub(now)))}
	}
	var hour, last int64
	for _, a := range g.st.Attempts {
		if a > now.Add(-time.Hour).UnixMilli() {
			hour++
		}
		last = max(last, a)
	}
	if last > 0 {
		if gap := now.Sub(time.UnixMilli(last)); gap < g.lim.MinGap {
			return &Refusal{fmt.Sprintf("领得太快：两次领取至少隔 %s，还要等 %s", wait(g.lim.MinGap), wait(g.lim.MinGap-gap))}
		}
	}
	if int(hour) >= g.lim.PerHour {
		return &Refusal{fmt.Sprintf("这一小时已经领了 %d 次，达到上限 %d；这是启动 hb mcp 的人定的上限，AI 不能改", hour, g.lim.PerHour)}
	}
	if len(g.st.Attempts) >= g.lim.PerDay {
		return &Refusal{fmt.Sprintf("最近 24 小时已经领了 %d 次，达到上限 %d；这是启动 hb mcp 的人定的上限，AI 不能改", len(g.st.Attempts), g.lim.PerDay)}
	}
	return nil
}

// Begin records an attempt before it is sent, so a crash cannot lose it.
func (g *Guard) Begin() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st.Attempts = append(g.st.Attempts, g.now().UnixMilli())
	g.save()
}

// Done records how the attempt went. A failure pauses claiming; enough
// failures in a row hold it for longer.
func (g *Guard) Done(ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if ok {
		g.st.Consecutive = 0
		g.save()
		return
	}
	g.st.Consecutive++
	d, why := g.lim.FailPause, "上一次领取没成功"
	if g.st.Consecutive >= g.lim.BreakAfter {
		d, why = g.lim.BreakFor, fmt.Sprintf("连续 %d 次没成功", g.st.Consecutive)
	}
	g.st.BlockedUntil = g.now().Add(d).UnixMilli()
	g.st.Reason = why
	g.save()
}

// Audit appends one line to the log of what the assistant did.
type Audit struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
}

func NewAudit(path string, now func() time.Time) *Audit {
	if now == nil {
		now = time.Now
	}
	return &Audit{path: path, now: now}
}

// Record writes one entry. It never carries a token, cookie or passcode.
func (a *Audit) Record(tool, packet, outcome string) {
	if a == nil || a.path == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if os.MkdirAll(filepath.Dir(a.path), 0o700) != nil {
		return
	}
	f, err := os.OpenFile(a.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]string{"at": a.now().UTC().Format(time.RFC3339), "tool": tool, "packet": packet, "outcome": outcome})
	_, _ = f.Write(append(b, '\n'))
}
