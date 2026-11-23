// Package config loads and saves the wgent user configuration file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const defaultPollSeconds = 2

// Config is the on-disk wgent configuration.
type Config struct {
	DefaultTunnel string `toml:"default_tunnel"`
	Tray          Tray   `toml:"tray"`
}

// Tray holds tray-icon settings.
type Tray struct {
	PollSeconds int `toml:"poll_seconds"`
}

// Path returns the config file location: $XDG_CONFIG_HOME/wgent/config.toml
// if XDG_CONFIG_HOME is set, else %USERPROFILE%\.config\wgent\config.toml.
func Path(getenv func(string) string) (string, error) {
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "wgent", "config.toml"), nil
	}
	home := getenv("USERPROFILE")
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
	}
	return filepath.Join(home, ".config", "wgent", "config.toml"), nil
}

// Load reads the config at path. A missing file yields defaults.
func Load(path string) (Config, error) {
	var c Config
	_, err := toml.DecodeFile(path, &c)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if c.Tray.PollSeconds <= 0 {
		c.Tray.PollSeconds = defaultPollSeconds
	}
	return c, nil
}

// Save writes c to path, creating parent directories.
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return f.Close()
}

// SetDefault sets default_tunnel, preserving the other settings.
func SetDefault(path, tunnel string) error {
	c, err := Load(path)
	if err != nil {
		return err
	}
	c.DefaultTunnel = tunnel
	return Save(path, c)
}
