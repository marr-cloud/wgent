//go:build windows

package svc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc/mgr"
)

func TestRemoveInstallDirGuard(t *testing.T) {
	for _, d := range []string{"wgent", t.TempDir(), ""} {
		if err := removeInstallDir(d); err == nil {
			t.Errorf("removeInstallDir(%q) should refuse", d)
		}
	}
	d := filepath.Join(t.TempDir(), "wgent")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "wgent.exe"), []byte("x"), 0o600)
	if err := removeInstallDir(d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d); !os.IsNotExist(err) {
		t.Fatal("dir still exists")
	}
}

func TestRecoveryActions(t *testing.T) {
	actions, reset := recoveryActions()
	want := []time.Duration{5 * time.Second, 10 * time.Second, 30 * time.Second}
	if len(actions) != len(want) || reset != 86400 {
		t.Fatalf("actions=%v reset=%d", actions, reset)
	}
	for i, a := range actions {
		if a.Type != mgr.ServiceRestart || a.Delay != want[i] {
			t.Errorf("action %d = %+v", i, a)
		}
	}
}

func TestRestartAfter(t *testing.T) {
	base := errors.New("copy failed")
	started := false
	err := restartAfter(func() error { started = true; return nil }, base)
	if !started || !errors.Is(err, base) || !strings.Contains(err.Error(), "previous service was restarted") {
		t.Fatalf("started=%v err=%v", started, err)
	}
	err = restartAfter(func() error { return errors.New("nope") }, base)
	if !errors.Is(err, base) || !strings.Contains(err.Error(), "also failed: nope") {
		t.Fatalf("err=%v", err)
	}
}
