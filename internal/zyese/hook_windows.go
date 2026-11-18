//go:build windows

package zyese

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The Windows hook: a low-level keyboard hook (WH_KEYBOARD_LL) on a thread of
// its own. It needs no administrator rights and no installation; it only sees
// keys while it is running, and it is the one place the system says whether a
// key was injected (LLKHF_INJECTED, or LLKHF_LOWER_IL_INJECTED from a process
// of lower integrity) or came from the keyboard.

const (
	whKeyboardLL       = 13
	wmKeyDown          = 0x0100
	wmKeyUp            = 0x0101
	wmSysKeyDown       = 0x0104
	wmSysKeyUp         = 0x0105
	wmQuit             = 0x0012
	llkhfInjected      = 0x10
	llkhfLowerILInject = 0x02
	hookEventBuffer    = 256
)

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	pSetWindowsHookEx = user32.NewProc("SetWindowsHookExW")
	pCallNextHookEx   = user32.NewProc("CallNextHookEx")
	pUnhook           = user32.NewProc("UnhookWindowsHookEx")
	pGetMessage       = user32.NewProc("GetMessageW")
	pPostThreadMsg    = user32.NewProc("PostThreadMessageW")
)

type kbdllHookStruct struct {
	VKCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      [2]int32
}

type winHook struct {
	ev    chan RawKey
	start time.Time
	tid   uint32
	once  sync.Once
	done  chan struct{}
}

var (
	current      atomic.Pointer[winHook]
	hookCallback uintptr
	hookCbOnce   sync.Once
)

func hookProc(nCode, wParam, lParam uintptr) uintptr {
	if int32(nCode) >= 0 {
		if h := current.Load(); h != nil {
			p := lParam
			k := (*kbdllHookStruct)(*(*unsafe.Pointer)(unsafe.Pointer(&p)))
			down := wParam == wmKeyDown || wParam == wmSysKeyDown
			up := wParam == wmKeyUp || wParam == wmSysKeyUp
			if down || up {
				ev := RawKey{VK: k.VKCode, Down: down, Injected: k.Flags&(llkhfInjected|llkhfLowerILInject) != 0, At: time.Since(h.start)}
				select {
				case h.ev <- ev:
				default: // nobody is reading: drop it rather than hold up every key in the system
				}
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, lParam)
	return r
}

// StartHook installs the hook. Only one runs at a time.
func StartHook() (Hook, error) {
	h := &winHook{ev: make(chan RawKey, hookEventBuffer), start: time.Now(), done: make(chan struct{})}
	if !current.CompareAndSwap(nil, h) {
		return nil, errors.New("按键钩子已经在运行")
	}
	hookCbOnce.Do(func() { hookCallback = syscall.NewCallback(hookProc) })
	ready := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(h.done)
		h.tid = windows.GetCurrentThreadId()
		hk, _, err := pSetWindowsHookEx.Call(whKeyboardLL, hookCallback, 0, 0)
		if hk == 0 {
			ready <- fmt.Errorf("装不上按键钩子：%v", err)
			return
		}
		ready <- nil
		var msg winMsg
		for {
			r, _, _ := pGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 { // WM_QUIT or an error
				break
			}
		}
		pUnhook.Call(hk)
	}()
	if err := <-ready; err != nil {
		current.Store(nil)
		return nil, err
	}
	return h, nil
}

func (h *winHook) Events() <-chan RawKey { return h.ev }
func (h *winHook) Now() time.Duration    { return time.Since(h.start) }

func (h *winHook) Close() {
	h.once.Do(func() {
		pPostThreadMsg.Call(uintptr(h.tid), wmQuit, 0, 0)
		select {
		case <-h.done:
		case <-time.After(2 * time.Second):
		}
		current.CompareAndSwap(h, nil)
	})
}

// SelfTest starts the hook, puts one harmless key in itself (F24, which no
// keyboard has and nothing reacts to) to check that injected keys are told
// apart, then counts for typeFor how many key presses are from the keyboard.
func SelfTest(typeFor time.Duration) (SelfTestResult, error) {
	var r SelfTestResult
	h, err := StartHook()
	if err != nil {
		return r, err
	}
	defer h.Close()
	kb := user32.NewProc("keybd_event")
	kb.Call(0x87, 0, 0, 0)
	kb.Call(0x87, 0, 2, 0)
	end := time.After(typeFor)
	for {
		select {
		case ev := <-h.Events():
			if !ev.Down {
				continue
			}
			switch {
			case ev.VK == 0x87:
				r.InjectedSeen = ev.Injected
			case ev.Injected:
				r.InjectedOther++
			default:
				r.Physical++
			}
		case <-end:
			return r, nil
		}
	}
}
