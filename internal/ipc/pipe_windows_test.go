//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/marr-cloud/wgent/internal/api"
)

func TestPipePath(t *testing.T) {
	if PipePath != "\\\\.\\pipe\\wgent" {
		t.Fatalf("PipePath = %q", PipePath)
	}
}

func currentSID(t *testing.T) string {
	t.Helper()
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return tu.User.Sid.String()
}

// testPipe serves echoHandler on a unique pipe. The server runs as the
// current user, so the SDDL must grant it FILE_CREATE_PIPE_INSTANCE (GA).
func testPipe(t *testing.T, suffix string) (path, sid string) {
	t.Helper()
	sid = currentSID(t)
	path = testPipePath(t) + suffix
	l, err := listenSDDL(path, "D:P(A;;GA;;;"+sid+")")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go Serve(ctx, l, echoHandler{})
	return path, sid
}

func testPipePath(t *testing.T) string {
	return fmt.Sprintf(`\\.\pipe\wgent-test-%d-%s`, os.Getpid(), t.Name())
}

func createInstance(path string) error {
	p16, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateNamedPipe(p16, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED,
		windows.PIPE_TYPE_BYTE|windows.PIPE_REJECT_REMOTE_CLIENTS, windows.PIPE_UNLIMITED_INSTANCES, 4096, 4096, 0, nil)
	if err == nil {
		windows.CloseHandle(h)
	}
	return err
}

func TestPipeSDDL(t *testing.T) {
	got := pipeSDDL("S-1-5-21-1-2-3-1001")
	want := "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x12019b;;;S-1-5-21-1-2-3-1001)"
	if got != want {
		t.Fatalf("sddl = %q", got)
	}
	if userPipeAccess&windows.FILE_APPEND_DATA != 0 { // FILE_CREATE_PIPE_INSTANCE
		t.Fatal("user ACE must not grant FILE_CREATE_PIPE_INSTANCE")
	}
}

func TestPipeRoundTrip(t *testing.T) {
	path, sid := testPipe(t, "")
	c := Client{Dial: func() (net.Conn, error) { return dial(path, sid) }}
	resp, err := c.Do(api.Request{Op: api.OpStatus, Tunnel: "home"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status == nil || resp.Status.Tunnel != "home" {
		t.Errorf("unexpected: %+v", resp)
	}
}

// A pipe served by anyone but LocalSystem must be refused (squatting).
func TestDialRefusesNonSystemServer(t *testing.T) {
	path, _ := testPipe(t, "")
	conn, err := dial(path, systemSID)
	if err == nil {
		conn.Close()
		t.Fatal("dial succeeded against a non-SYSTEM server")
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != api.CodeServiceNotInstalled ||
		!strings.Contains(apiErr.Message, "pipe server is not the wgent service") {
		t.Fatalf("err = %#v", err)
	}
}

// The authorized user must not be able to add server instances to the pipe
// with the production SDDL, while a GA grant would allow it (control).
func TestUserCannotCreatePipeInstance(t *testing.T) {
	path := testPipePath(t)
	l, err := listen(path, currentSID(t))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := createInstance(path); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("production SDDL: err = %v, want access denied", err)
	}

	ctrl, _ := testPipe(t, "-ctrl")
	if err := createInstance(ctrl); err != nil {
		t.Fatalf("control (GA): %v", err)
	}
}

func TestDialMissingPipe(t *testing.T) {
	_, err := dial(`\\.\pipe\wgent-test-does-not-exist`, systemSID)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != api.CodeServiceNotInstalled {
		t.Fatalf("err = %#v", err)
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
