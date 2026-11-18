package zyese

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// A keyboard hook is where a typed key gets a real basis for "trusted": the
// operating system says whether the key came from the keyboard or was put in
// by a program (on Windows, the LLKHF_INJECTED flag of a low-level hook). The
// browser's isTrusted means the same thing, vouched for by the browser.
//
// What it keeps is the least that is needed to match a key the terminal
// delivered with one the keyboard produced: which physical key, down or up,
// whether it was injected, and when. It lives in memory for about a second;
// nothing is written to disk or a log, and a key the game did not receive is
// never sent anywhere. It is only started on request (--input-telemetry), only
// for a run, and stopped with it.

// RawKey is one physical key event from the hook.
type RawKey struct {
	VK       uint32
	Down     bool
	Injected bool          // put in by a program, not the keyboard
	At       time.Duration // on the hook's clock
}

// Hook delivers physical key events.
type Hook interface {
	Events() <-chan RawKey
	// Now is the hook's clock; keys the terminal delivers are timed on it too.
	Now() time.Duration
	Close()
}

// ErrHookUnsupported: no hook for this system yet.
var ErrHookUnsupported = errors.New("这个系统上还没有按键钩子（现在只有 Windows）")

// Telemetry is what goes with one key in input telemetry version 1.
type Telemetry struct {
	EventID int
	Source  string // "physical" or "ime"
	Trusted bool
	Code    string // "KeyA", "Space" ...
	Repeat  bool
	EventAt float64 // ms on the hook's clock
}

// vkOf is the virtual key that types r on a US-style layout: letters and
// space. Anything else (an IME commit, punctuation) has none.
func vkOf(r rune) (uint32, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return uint32(r - 'a' + 'A'), true
	case r >= 'A' && r <= 'Z':
		return uint32(r), true
	case r >= '0' && r <= '9':
		return uint32(r), true
	case r == ' ':
		return 0x20, true
	}
	return 0, false
}

// codeOf is the DOM `code` of a virtual key, for the ones vkOf gives.
func codeOf(vk uint32) string {
	switch {
	case vk >= 'A' && vk <= 'Z':
		return "Key" + string(rune(vk))
	case vk >= '0' && vk <= '9':
		return "Digit" + string(rune(vk))
	case vk == 0x20:
		return "Space"
	}
	return ""
}

// Matcher pairs the characters the terminal delivers with the key events the
// hook saw. A character with a matching key down that was not injected is
// trusted; one with none, or with an injected one, is not, and is said so.
type Matcher struct {
	mu     sync.Mutex
	src    <-chan RawKey
	downs  []RawKey        // key downs not yet matched, oldest first
	held   map[uint32]bool // keys physically held now
	active map[uint32]heldKey
	ups    []KeyUp
	nextID int
	stop   chan struct{}
}

type heldKey struct {
	id      int
	key     string
	code    string
	trusted bool
}

// KeyUp is a key released that an earlier key event was sent for.
type KeyUp struct {
	EventID int
	Key     string
	Code    string
	Trusted bool
	EventAt float64
}

const (
	matchBefore = 600 * time.Millisecond // the keyboard event comes first, the terminal's a little later
	matchAfter  = 100 * time.Millisecond
	matchWait   = 30 * time.Millisecond // how long a key waits for its keyboard event to arrive
)

func NewMatcher() *Matcher {
	return &Matcher{held: map[uint32]bool{}, active: map[uint32]heldKey{}, stop: make(chan struct{})}
}

// Attach feeds the matcher from a hook's events until Stop.
func (m *Matcher) Attach(src <-chan RawKey) {
	m.src = src
	go func() {
		for {
			select {
			case ev := <-src:
				m.Feed(ev)
			case <-m.stop:
				return
			}
		}
	}()
}

func (m *Matcher) Stop() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
}

// drain takes what the hook has already said, so a key does not outrun its own event.
func (m *Matcher) drain() {
	for {
		select {
		case ev := <-m.src:
			m.Feed(ev)
		default:
			return
		}
	}
}

// Ups are the key releases to send, in order; they are handed over once.
func (m *Matcher) Ups() []KeyUp {
	m.mu.Lock()
	defer m.mu.Unlock()
	u := m.ups
	m.ups = nil
	return u
}

// Feed takes a hook event.
func (m *Matcher) Feed(ev RawKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ev.Down {
		m.downs = append(m.downs, ev)
		if len(m.downs) > 256 {
			m.downs = m.downs[len(m.downs)-256:]
		}
		return
	}
	delete(m.held, ev.VK)
	if a, ok := m.active[ev.VK]; ok {
		delete(m.active, ev.VK)
		m.ups = append(m.ups, KeyUp{EventID: a.id, Key: a.key, Code: a.code, Trusted: a.trusted && !ev.Injected, EventAt: ms(ev.At)})
	}
}

// Take pairs a typed character, delivered at now, with a key down.
func (m *Matcher) Take(r rune, now func() time.Duration) Telemetry {
	vk, known := vkOf(r)
	if known && m.src != nil {
		for waited := time.Duration(0); ; waited += 5 * time.Millisecond {
			m.drain()
			if t, ok := m.match(r, vk, now()); ok {
				return t
			}
			if waited >= matchWait {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	t := Telemetry{EventID: m.nextID, Source: "physical", EventAt: ms(now())}
	if !known {
		t.Source = "ime" // an input method commit, punctuation: no physical key to vouch for it
		return t
	}
	t.Code = codeOf(vk)
	return t // the terminal got a key the keyboard did not produce
}

func (m *Matcher) match(r rune, vk uint32, at time.Duration) (Telemetry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for len(m.downs) > 0 && at-m.downs[0].At > matchBefore*2 { // too old to match anything
		m.downs = m.downs[1:]
	}
	for i, d := range m.downs {
		if d.VK != vk || d.At < at-matchBefore || d.At > at+matchAfter {
			continue
		}
		m.downs = append(m.downs[:i:i], m.downs[i+1:]...)
		m.nextID++
		t := Telemetry{EventID: m.nextID, Source: "physical", Code: codeOf(vk), EventAt: ms(d.At), Trusted: !d.Injected, Repeat: m.held[vk]}
		m.held[vk] = true
		if !t.Repeat {
			m.active[vk] = heldKey{id: t.EventID, key: strings.ToLower(string(r)), code: t.Code, trusted: t.Trusted}
		}
		return t, true
	}
	return Telemetry{}, false
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// SelfTestResult is what the hook did in a self-test. Keys are only counted:
// which key it was is not looked at.
type SelfTestResult struct {
	InjectedSeen  bool // a key the test put in itself was seen, and flagged injected
	Physical      int  // key presses not flagged injected, while the person typed
	InjectedOther int  // key presses flagged injected that the test did not make
}
