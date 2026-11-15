package auth

import (
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

func TestSaveLoadDelete(t *testing.T) {
	keyring.MockInit()
	s := Session{Provider: "lafish", CSRF: "t", Cookies: []Cookie{{Name: "hb_session", Value: "v"}}}
	if err := Save("default", s); err != nil {
		t.Fatal(err)
	}
	got, err := Load("default", "lafish")
	if err != nil || got.CSRF != "t" || got.Cookies[0].Value != "v" || got.SavedAt.IsZero() {
		t.Fatalf("got %+v err %v", got, err)
	}
	if got.CookieHeader() != "hb_session=v" {
		t.Fatalf("header %q", got.CookieHeader())
	}
	if err := Delete("default", "lafish"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("default", "lafish"); err != ErrNoSession {
		t.Fatalf("err %v", err)
	}
	if err := Delete("default", "lafish"); err != nil {
		t.Fatalf("second delete should be a no-op: %v", err)
	}
}

func TestExpired(t *testing.T) {
	now := time.Unix(1000, 0)
	if (Session{Cookies: []Cookie{{Expires: 0}}}).Expired(now) {
		t.Error("session cookie must not be reported expired")
	}
	if !(Session{Cookies: []Cookie{{Expires: 999}}}).Expired(now) {
		t.Error("past expiry should be expired")
	}
	if (Session{Cookies: []Cookie{{Expires: 999}, {Expires: 2000}}}).Expired(now) {
		t.Error("one live cookie keeps the session alive")
	}
}
