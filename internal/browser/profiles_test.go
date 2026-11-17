package browser

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProfilesComeFromLocalStateAndExist(t *testing.T) {
	ud := t.TempDir()
	for _, d := range []string{"Default", "Profile 2"} {
		if err := os.MkdirAll(filepath.Join(ud, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ls := `{"profile":{"info_cache":{"Profile 2":{"name":"工作"},"Default":{"name":"个人"},"Profile 9":{"name":"删掉了"}}}}`
	if err := os.WriteFile(filepath.Join(ud, "Local State"), []byte(ls), 0o600); err != nil {
		t.Fatal(err)
	}
	ps, err := Profiles(ud)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].Dir != "Default" || ps[0].Name != "个人" || ps[1].Dir != "Profile 2" {
		t.Fatalf("%+v", ps)
	}
}

func TestCloneRefusesAMissingProfileAndLeavesNoHalfCopy(t *testing.T) {
	ud := t.TempDir()
	_ = os.WriteFile(filepath.Join(ud, "Local State"), []byte("{}"), 0o600)
	dst := filepath.Join(t.TempDir(), "clone")
	if err := CloneProfile(ud, "Nope", dst); !errors.Is(err, ErrNoProfile) {
		t.Fatalf("missing profile: %v", err)
	}
	// the cookie database exists but cannot be read as a file (a folder in its place): the copy fails midway
	if err := os.MkdirAll(filepath.Join(ud, "Default", "Network", "Cookies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CloneProfile(ud, "Default", dst); err == nil {
		t.Fatal("expected a failure")
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a failed copy must not leave Local State or anything else behind")
	}
}
