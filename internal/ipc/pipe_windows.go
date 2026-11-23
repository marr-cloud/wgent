//go:build windows

package ipc

import (
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

// Listen creates the pipe, accessible to SYSTEM, Administrators and userSID.
func Listen(userSID string) (net.Listener, error) {
	if err := ValidateSID(userSID); err != nil {
		return nil, err
	}
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
