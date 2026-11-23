//go:build windows

package svc

import (
	"os"
	"path/filepath"
	"testing"
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
