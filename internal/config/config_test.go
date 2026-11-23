package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathXDG(t *testing.T) {
	got, err := Path(func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return `C:\x`
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(`C:\x`, "wgent", "config.toml")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPathUserProfile(t *testing.T) {
	got, err := Path(func(k string) string {
		if k == "USERPROFILE" {
			return `C:\Users\u`
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(`C:\Users\u`, ".config", "wgent", "config.toml")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nope", "config.toml")
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c != (Config{Tray: Tray{PollSeconds: 2}}) {
		t.Fatalf("unexpected config %+v", c)
	}
}

func TestLoadMalformed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("default_tunnel = "), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), p) {
		t.Fatalf("expected error containing path, got %v", err)
	}
}

func TestLoadValues(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("default_tunnel = \"a-ado-cloud\"\n[tray]\npoll_seconds = 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultTunnel != "a-ado-cloud" || c.Tray.PollSeconds != 5 {
		t.Fatalf("unexpected config %+v", c)
	}
}

func TestSetDefaultCreatesFileAndKeepsTray(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.toml")
	if err := Save(p, Config{Tray: Tray{PollSeconds: 7}}); err != nil {
		t.Fatal(err)
	}
	if err := SetDefault(p, "work"); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultTunnel != "work" || c.Tray.PollSeconds != 7 {
		t.Fatalf("unexpected config %+v", c)
	}
}
