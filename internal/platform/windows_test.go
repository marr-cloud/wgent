//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func newTestWindows(t *testing.T, files ...string) *Windows {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "Data", "Configurations")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(cfg, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &Windows{WireGuardDir: dir}
}

func TestConfigNames(t *testing.T) {
	w := newTestWindows(t, "a.conf.dpapi", "a.conf", "b.conf", "notes.txt")
	got, err := w.ConfigNames()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestConfigNamesMissingDir(t *testing.T) {
	w := &Windows{WireGuardDir: t.TempDir()}
	got, err := w.ConfigNames()
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestConfigPathPrefersDPAPI(t *testing.T) {
	w := newTestWindows(t, "a.conf.dpapi", "a.conf", "b.conf")
	p, err := w.ConfigPath("a")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(w.WireGuardDir, "Data", "Configurations", "a.conf.dpapi"); p != want {
		t.Fatalf("got %s want %s", p, want)
	}
	p, err = w.ConfigPath("b")
	if err != nil || !strings.HasSuffix(p, "b.conf") {
		t.Fatalf("got %s, %v", p, err)
	}
	if _, err := w.ConfigPath("zzz"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

func TestInstallTunnelArgs(t *testing.T) {
	w := &Windows{WireGuardDir: `C:\WG`}
	var gotName string
	var gotArgs []string
	w.Run = func(name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return nil, nil
	}
	if err := w.InstallTunnel(`C:\x\a.conf`); err != nil {
		t.Fatal(err)
	}
	if gotName != filepath.Join(`C:\WG`, "wireguard.exe") {
		t.Fatalf("name %s", gotName)
	}
	if want := []string{"/installtunnelservice", `C:\x\a.conf`}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args %v", gotArgs)
	}
	if err := w.UninstallTunnel("a"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/uninstalltunnelservice", "a"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args %v", gotArgs)
	}
}

func TestDumpArgsAndError(t *testing.T) {
	w := &Windows{WireGuardDir: `C:\WG`}
	var gotName string
	var gotArgs []string
	w.Run = func(name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return []byte("Permission denied\n"), errors.New("exit status 1")
	}
	_, err := w.Dump("a")
	if err == nil || !strings.Contains(err.Error(), "Permission denied") {
		t.Fatalf("err %v", err)
	}
	if gotName != filepath.Join(`C:\WG`, "wg.exe") {
		t.Fatalf("name %s", gotName)
	}
	if want := []string{"show", "a", "dump"}; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("args %v", gotArgs)
	}
}

func TestResolveProgramFiles(t *testing.T) {
	bad := func() (string, error) { return "", errors.New("no") }
	good := func() (string, error) { return `C:\Program Files`, nil }
	env := func(v string) func(string) string { return func(string) string { return v } }
	if p, err := resolveProgramFiles(good, env("")); err != nil || p != `C:\Program Files` {
		t.Fatal(p, err)
	}
	if p, err := resolveProgramFiles(bad, env(`D:\PF`)); err != nil || p != `D:\PF` {
		t.Fatal(p, err)
	}
	if _, err := resolveProgramFiles(bad, env("")); err == nil {
		t.Fatal("want error for empty")
	}
	if _, err := resolveProgramFiles(bad, env(`rel\path`)); err == nil {
		t.Fatal("want error for relative")
	}
}

func TestRunErrorNoDanglingColon(t *testing.T) {
	w := &Windows{WireGuardDir: `C:\WG`, Run: func(string, ...string) ([]byte, error) { return nil, errors.New("boom") }}
	err := w.UninstallTunnel("a")
	if err == nil || strings.HasSuffix(err.Error(), ": ") {
		t.Fatal(err)
	}
}
