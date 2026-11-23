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
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/marr-cloud/wgent/internal/api"
)

const PipePath = `\\.\pipe\wgent`

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

// ServiceName is the wgent Windows service (the svc package installs it).
const ServiceName = "wgent"

// ServiceSDDL is the DACL set on the wgent service: the Windows defaults plus
// query rights for the installing user, so its Dial can verify the pipe server
// even from a non-interactive logon (e.g. SSH), where IU does not apply.
func ServiceSDDL(userSID string) string {
	return "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)" +
		"(A;;CCLCSWLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)(A;;CCLCSWLOCRRC;;;" + userSID + ")"
}

// Dial connects to the service pipe, verifies that the server process is the
// wgent service running as LocalSystem and maps common failures to *api.Error.
func Dial() (net.Conn, error) { return dial(PipePath, verifyService) }

func notRunning(msg string) *api.Error {
	return &api.Error{Code: api.CodeServiceNotInstalled, Message: msg, Hint: installHint}
}

const notService = "pipe server is not the wgent service"

func dial(path string, verify func(pid uint32) error) (net.Conn, error) {
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
	if err := checkServer(conn, verify); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// checkServer refuses a pipe whose server process fails verify, so a process
// that grabbed the pipe name cannot impersonate the service.
func checkServer(conn net.Conn, verify func(pid uint32) error) error {
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return notRunning(notService + ": pipe connection has no handle")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(fd.Fd()), &pid); err != nil {
		return notRunning(notService + ": cannot get the pipe server process: " + err.Error())
	}
	return verify(pid)
}

// verifyService requires pid to be the process of the wgent service and the
// service to be configured as LocalSystem (S-1-5-18). A standard user cannot
// open a SYSTEM process token, so the SCM is the authority on identity: only
// an administrator can change which process is the wgent service.
func verifyService(pid uint32) error {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return notRunning(notService + ": connect to service manager: " + err.Error())
	}
	defer windows.CloseServiceHandle(m)
	name, _ := windows.UTF16PtrFromString(ServiceName)
	s, err := windows.OpenService(m, name, windows.SERVICE_QUERY_STATUS|windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return notRunning(notService + " (the wgent service is not installed)")
		}
		return notRunning(notService + ": open service: " + err.Error())
	}
	defer windows.CloseServiceHandle(s)

	var st windows.SERVICE_STATUS_PROCESS
	var need uint32
	if err := windows.QueryServiceStatusEx(s, windows.SC_STATUS_PROCESS_INFO, (*byte)(unsafe.Pointer(&st)), uint32(unsafe.Sizeof(st)), &need); err != nil {
		return notRunning(notService + ": query service: " + err.Error())
	}
	if st.ProcessId == 0 || st.ProcessId != pid {
		return notRunning(fmt.Sprintf("%s (pipe server pid %d, wgent service pid %d)", notService, pid, st.ProcessId))
	}
	account, err := serviceAccount(s)
	if err != nil {
		return notRunning(notService + ": " + err.Error())
	}
	if !strings.EqualFold(account, "LocalSystem") && account != `NT AUTHORITY\SYSTEM` {
		return notRunning(fmt.Sprintf("%s (service runs as %q, want LocalSystem)", notService, account))
	}
	return nil
}

func serviceAccount(s windows.Handle) (string, error) {
	var need uint32
	err := windows.QueryServiceConfig(s, nil, 0, &need)
	if err != nil && !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return "", fmt.Errorf("query service config: %w", err)
	}
	buf := make([]uint64, need/8+1) // 8-byte aligned
	cfg := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buf[0]))
	if err := windows.QueryServiceConfig(s, cfg, uint32(len(buf)*8), &need); err != nil {
		return "", fmt.Errorf("query service config: %w", err)
	}
	return windows.UTF16PtrToString(cfg.ServiceStartName), nil
}
