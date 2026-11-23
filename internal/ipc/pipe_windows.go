//go:build windows

package ipc

import (
	"errors"
	"net"
	"os"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/marr-cloud/wgent/internal/api"
)

const PipePath = `\.\pipe\wgent`

// Listen creates the pipe, accessible to SYSTEM, Administrators and userSID.
func Listen(userSID string) (net.Listener, error) {
	sddl := "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;" + userSID + ")"
	return winio.ListenPipe(PipePath, &winio.PipeConfig{SecurityDescriptor: sddl})
}

// Dial connects to the service pipe, mapping common failures to *api.Error.
func Dial() (net.Conn, error) {
	timeout := 2 * time.Second
	conn, err := winio.DialPipe(PipePath, &timeout)
	if err == nil {
		return conn, nil
	}
	switch {
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, os.ErrNotExist), errors.Is(err, os.ErrDeadlineExceeded):
		return nil, &api.Error{Code: api.CodeServiceNotInstalled, Message: "wgent service is not running", Hint: "sudo wgent service install"}
	case errors.Is(err, windows.ERROR_ACCESS_DENIED), errors.Is(err, os.ErrPermission):
		return nil, &api.Error{Code: api.CodeServiceNotInstalled, Message: "access to wgent service denied (installed for another user?)", Hint: "sudo wgent service install"}
	}
	return nil, err
}
