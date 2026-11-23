//go:build windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/marr-cloud/wgent/internal/api"
)

const PipePath = `\\.\pipe\wgent`

// systemSID is LocalSystem, the account the wgent service runs as.
const systemSID = "S-1-5-18"

// userPipeAccess is the access granted to the authorized user and requested
// by the client: FILE_GENERIC_READ | FILE_GENERIC_WRITE without
// FILE_APPEND_DATA (0x4), which on a pipe is FILE_CREATE_PIPE_INSTANCE. The
// user can talk to the service but cannot add server instances to its pipe.
const userPipeAccess = 0x12019b

// ValidateSID requires a canonical SID string (S-1-...), rejecting SDDL
// aliases and anything that could alter the security descriptor.
func ValidateSID(s string) error {
	if !strings.HasPrefix(s, "S-1-") {
		return fmt.Errorf("invalid SID %q", s)
	}
	sid, err := windows.StringToSid(s)
	if err != nil || sid.String() != s {
		return fmt.Errorf("invalid SID %q", s)
	}
	return nil
}

// pipeSDDL grants full access to SYSTEM and Administrators and read/write
// (but not FILE_CREATE_PIPE_INSTANCE) to userSID.
func pipeSDDL(userSID string) string {
	return fmt.Sprintf("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;%#x;;;%s)", userPipeAccess, userSID)
}

// Listen creates the pipe, accessible to SYSTEM, Administrators and userSID.
func Listen(userSID string) (net.Listener, error) { return listen(PipePath, userSID) }

func listen(path, userSID string) (net.Listener, error) {
	if err := ValidateSID(userSID); err != nil {
		return nil, err
	}
	return listenSDDL(path, pipeSDDL(userSID))
}

// listenSDDL creates the pipe with an explicit security descriptor. The
// server's own account needs FILE_CREATE_PIPE_INSTANCE (go-winio opens a new
// instance per Accept); in production that account is SYSTEM.
func listenSDDL(path, sddl string) (net.Listener, error) {
	return winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: sddl})
}

// Dial connects to the service pipe, verifies that the server process runs as
// LocalSystem and maps common failures to *api.Error.
func Dial() (net.Conn, error) { return dial(PipePath, systemSID) }

func notRunning(msg string) *api.Error {
	return &api.Error{Code: api.CodeServiceNotInstalled, Message: msg, Hint: installHint}
}

func dial(path, wantSID string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := winio.DialPipeAccess(ctx, path, userPipeAccess)
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, os.ErrNotExist):
			return nil, notRunning("wgent service is not running")
		case errors.Is(err, context.DeadlineExceeded), errors.Is(err, winio.ErrTimeout), errors.Is(err, os.ErrDeadlineExceeded):
			return nil, notRunning("wgent service not responding")
		case errors.Is(err, windows.ERROR_ACCESS_DENIED), errors.Is(err, os.ErrPermission):
			return nil, notRunning("access to wgent service denied (installed for another user?)")
		}
		return nil, err
	}
	if err := checkServer(conn, wantSID); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// checkServer refuses a pipe whose server process does not run as wantSID,
// so a squatter cannot impersonate the service.
func checkServer(conn net.Conn, wantSID string) error {
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return fmt.Errorf("pipe connection has no handle")
	}
	got, err := serverSID(windows.Handle(fd.Fd()))
	if err != nil {
		return notRunning("cannot verify the wgent service identity: " + err.Error())
	}
	if got != wantSID {
		return notRunning(fmt.Sprintf("pipe server is not the wgent service (runs as %s, want %s)", got, wantSID))
	}
	return nil
}

func serverSID(pipe windows.Handle) (string, error) {
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(pipe, &pid); err != nil {
		return "", fmt.Errorf("get pipe server process: %w", err)
	}
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", fmt.Errorf("open pipe server process: %w", err)
	}
	defer windows.CloseHandle(p)
	var tok windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY, &tok); err != nil {
		return "", fmt.Errorf("open pipe server token: %w", err)
	}
	defer tok.Close()
	tu, err := tok.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("read pipe server token: %w", err)
	}
	return tu.User.Sid.String(), nil
}
