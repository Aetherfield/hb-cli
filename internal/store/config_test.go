package store

import "testing"

func TestConfigRoundTrip(t *testing.T) {
	t.Setenv("HB_HOME", t.TempDir())

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultProfile != "default" {
		t.Fatalf("default profile = %q", c.DefaultProfile)
	}

	c.Lang = "en"
	c.Limits = Limits{PerCommand: 5, Daily: 20}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != c {
		t.Fatalf("got %+v want %+v", got, c)
	}
}
