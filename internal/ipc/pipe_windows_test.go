//go:build windows

package ipc

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/marr-cloud/wgent/internal/api"
)

func TestPipePath(t *testing.T) {
	if PipePath != "\\\\.\\pipe\\wgent" {
		t.Fatalf("PipePath = %q", PipePath)
	}
}

func TestPipeRoundTrip(t *testing.T) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	l, err := Listen(tu.User.Sid.String())
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Skip("cannot create pipe unelevated: ", err)
		}
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, l, echoHandler{})
	resp, err := Client{Dial: Dial}.Do(api.Request{Op: api.OpStatus, Tunnel: "home"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status == nil || resp.Status.Tunnel != "home" {
		t.Errorf("unexpected: %+v", resp)
	}
}

func TestListenRejectsBadSID(t *testing.T) {
	for _, s := range []string{"WD", `S-1-5-21-1)(A;;GA;;;WD`, ""} {
		if l, err := Listen(s); err == nil {
			l.Close()
			t.Errorf("Listen(%q) succeeded", s)
		}
	}
	if err := ValidateSID("S-1-5-18"); err != nil {
		t.Errorf("valid SID rejected: %v", err)
	}
}
