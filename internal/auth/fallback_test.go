package auth

import (
	"errors"
	"os"
	"runtime"
	"testing"

	"github.com/zalando/go-keyring"
)

func sess() Session {
	return Session{Provider: "lafish", CSRF: "t", Cookies: []Cookie{{Name: "hb_session", Value: "v"}}}
}

func TestKeychainIsUsedWhenItWorks(t *testing.T) {
	t.Setenv("HB_HOME", t.TempDir())
	keyring.MockInit()
	if err := Save("default", sess()); err != nil {
		t.Fatal(err)
	}
	if LastBackend() != BackendKeychain {
		t.Fatalf("backend=%s", LastBackend())
	}
	if p, _ := FallbackPath(); fileExists(p) {
		t.Fatal("no file may be written when the keychain works")
	}
}

func TestNoKeychainFallsBackToAnOwnerOnlyFile(t *testing.T) {
	t.Setenv("HB_HOME", t.TempDir())
	keyring.MockInitWithError(errors.New("no secret service"))
	if err := Save("default", sess()); err != nil {
		t.Fatalf("login must still work without a keychain: %v", err)
	}
	if LastBackend() != BackendFile {
		t.Fatalf("backend=%s", LastBackend())
	}
	p, _ := FallbackPath()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("the session file must be owner-only, got %v", st.Mode().Perm())
	}
	got, err := Load("default", "lafish")
	if err != nil || got.CSRF != "t" || got.Cookies[0].Value != "v" {
		t.Fatalf("got %+v err %v", got, err)
	}
	if err := Delete("default", "lafish"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("default", "lafish"); err == nil {
		t.Fatal("deleted session still loads")
	}
	if fileExists(p) {
		t.Fatal("an empty session file should be removed")
	}
}

func TestSessionsSavedToTheFileAreFoundOnceTheKeychainWorks(t *testing.T) {
	t.Setenv("HB_HOME", t.TempDir())
	keyring.MockInitWithError(errors.New("no secret service"))
	if err := Save("default", sess()); err != nil {
		t.Fatal(err)
	}
	keyring.MockInit() // a keychain appears later: the old session is still there
	if got, err := Load("default", "lafish"); err != nil || got.CSRF != "t" {
		t.Fatalf("got %+v err %v", got, err)
	}
	// saving again moves it into the keychain and removes the file copy
	if err := Save("default", sess()); err != nil {
		t.Fatal(err)
	}
	if p, _ := FallbackPath(); fileExists(p) {
		t.Fatal("the stale file copy must be removed")
	}
}

func TestSeveralSessionsShareTheFile(t *testing.T) {
	t.Setenv("HB_HOME", t.TempDir())
	keyring.MockInitWithError(errors.New("no secret service"))
	a, b := sess(), sess()
	b.Provider = "unsnow"
	b.CSRF = "other"
	if err := Save("default", a); err != nil {
		t.Fatal(err)
	}
	if err := Save("default", b); err != nil {
		t.Fatal(err)
	}
	ga, _ := Load("default", "lafish")
	gb, _ := Load("default", "unsnow")
	if ga.CSRF != "t" || gb.CSRF != "other" {
		t.Fatalf("%+v %+v", ga, gb)
	}
	_ = Delete("default", "lafish")
	if _, err := Load("default", "unsnow"); err != nil {
		t.Fatal("deleting one must not touch the other")
	}
}

func TestNoSessionAnywhere(t *testing.T) {
	t.Setenv("HB_HOME", t.TempDir())
	keyring.MockInit()
	if _, err := Load("default", "lafish"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("err=%v", err)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
