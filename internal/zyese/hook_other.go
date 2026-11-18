//go:build !windows

package zyese

import "time"

// StartHook: only Windows has a hook so far (see hook.go).
func StartHook() (Hook, error) { return nil, ErrHookUnsupported }

// SelfTest: see hook.go.
func SelfTest(typeFor time.Duration) (SelfTestResult, error) {
	return SelfTestResult{}, ErrHookUnsupported
}
