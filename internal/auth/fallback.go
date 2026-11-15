package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"hb/internal/store"
)

// Where the last Save went: the OS keychain, or the fallback file.
const (
	BackendKeychain = "keychain"
	BackendFile     = "file"
)

var (
	fallbackMu  sync.Mutex
	lastBackend = BackendKeychain
)

// LastBackend says where the most recent Save put the session. Callers warn
// when it is BackendFile: that file is not encrypted.
func LastBackend() string {
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	return lastBackend
}

func setBackend(b string) {
	fallbackMu.Lock()
	lastBackend = b
	fallbackMu.Unlock()
}

// FallbackPath is the file used when the OS keychain is not available
// (a Linux box with no Secret Service, an SSH session, a container).
func FallbackPath() (string, error) {
	h, err := store.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "sessions.json"), nil
}

func readFallback() (map[string]Session, error) {
	p, err := FallbackPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Session{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]Session{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// writeFallback replaces the file atomically, owner-only.
func writeFallback(m map[string]Session) error {
	p, err := FallbackPath()
	if err != nil {
		return err
	}
	if len(m) == 0 {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func saveFallback(k string, s Session) error {
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	m, err := readFallback()
	if err != nil {
		m = map[string]Session{} // unreadable: start over rather than refuse to log in
	}
	m[k] = s
	return writeFallback(m)
}

func loadFallback(k string) (Session, bool) {
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	m, err := readFallback()
	if err != nil {
		return Session{}, false
	}
	s, ok := m[k]
	return s, ok
}

func deleteFallback(k string) {
	fallbackMu.Lock()
	defer fallbackMu.Unlock()
	m, err := readFallback()
	if err != nil {
		return
	}
	if _, ok := m[k]; ok {
		delete(m, k)
		_ = writeFallback(m)
	}
}
