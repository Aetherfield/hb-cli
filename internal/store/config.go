// Package store holds on-disk state: the TOML config (and, later, the
// keychain wrapper and SQLite ledger).
package store

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Limits are the spend guardrails shared by send/transfer/points/move.
// Zero means "not set", which commands treat as "refuse to spend".
type Limits struct {
	PerCommand float64 `toml:"per_command"`
	Daily      float64 `toml:"daily"`
}

type Config struct {
	Lang           string `toml:"lang"`
	DefaultProfile string `toml:"default_profile"`
	Limits         Limits `toml:"limits"`
}

// Home returns the hb data directory: $HB_HOME, else the user config dir + "hb".
func Home() (string, error) {
	if h := os.Getenv("HB_HOME"); h != "" {
		return h, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "hb"), nil
}

func configPath() (string, error) {
	h, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, "config.toml"), nil
}

// Default returns the config used when no file exists yet.
func Default() Config {
	return Config{DefaultProfile: "default"}
}

// Load reads the config; a missing file yields Default().
func Load() (Config, error) {
	p, err := configPath()
	if err != nil {
		return Config{}, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	c := Default()
	if err := toml.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Save writes the config atomically with owner-only permissions.
func Save(c Config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := toml.Marshal(c)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
