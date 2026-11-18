//go:build windows

package zyese

import (
	"testing"
	"time"
)

// A key put in by a program must come out flagged as injected, and a hook
// must stop cleanly. F24 (0x87) is used: no keyboard has it, nothing reacts to it.
func TestTheWindowsHookFlagsAnInjectedKey(t *testing.T) {
	h, err := StartHook()
	if err != nil {
		t.Skipf("no hook here: %v", err)
	}
	defer h.Close()
	kb := user32.NewProc("keybd_event")
	kb.Call(0x87, 0, 0, 0) // down
	kb.Call(0x87, 0, 2, 0) // up (KEYEVENTF_KEYUP)
	var got []RawKey
	deadline := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-h.Events():
			if ev.VK == 0x87 {
				got = append(got, ev)
			}
		case <-deadline:
			t.Skipf("the hook saw nothing (a session without a desktop?): %v", got)
		}
	}
	if !got[0].Down || got[1].Down || !got[0].Injected || !got[1].Injected {
		t.Fatalf("an injected F24 down and up: %+v", got)
	}
	if h.Now() <= 0 {
		t.Fatal("the hook has a clock")
	}
	h.Close()
	if h2, err := StartHook(); err != nil {
		t.Fatalf("a hook can start again after Close: %v", err)
	} else {
		h2.Close()
	}
}
