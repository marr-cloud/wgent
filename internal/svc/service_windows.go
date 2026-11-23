//go:build windows

// Package svc installs, removes and runs the wgent Windows service.
package svc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	winsvc "golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/marr-cloud/wgent/internal/daemon"
	"github.com/marr-cloud/wgent/internal/ipc"
	"github.com/marr-cloud/wgent/internal/platform"
)

const (
	serviceName = "wgent"
	displayName = "wgent (WireGuard for agents)"
	description = "Lets non-elevated users and AI agents start/stop WireGuard tunnels."
)

func installDir() string  { return filepath.Join(os.Getenv("ProgramFiles"), "wgent") }
func installPath() string { return filepath.Join(installDir(), "wgent.exe") }

// CurrentUserSID returns the SID string of the current process's user.
func CurrentUserSID() (string, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("get token user: %w", err)
	}
	return u.User.Sid.String(), nil
}

// IsElevated reports whether the current process token is elevated.
func IsElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

func waitState(s *mgr.Service, want winsvc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("query service: %w", err)
		}
		if st.State == want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for service state %d (current %d)", timeout, want, st.State)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func copyExe(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "wgent-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("copy executable: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("write executable: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("replace %s: %w", dst, err)
	}
	return nil
}

func sameFile(a, b string) bool {
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ai, bi)
}

// Install copies selfPath into %ProgramFiles%\wgent, creates or updates the
// wgent service for userSID and starts it. Requires elevation.
func Install(selfPath, userSID string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager (run as administrator): %w", err)
	}
	defer m.Disconnect()

	dst := installPath()
	s, err := m.OpenService(serviceName)
	exists := err == nil
	if exists {
		defer s.Close()
		st, qerr := s.Query()
		if qerr != nil {
			return fmt.Errorf("query service: %w", qerr)
		}
		if st.State != winsvc.Stopped {
			if st.State != winsvc.StopPending {
				if _, err := s.Control(winsvc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
					return fmt.Errorf("stop existing service: %w", err)
				}
			}
			if err := waitState(s, winsvc.Stopped, 20*time.Second); err != nil {
				return fmt.Errorf("stop existing service: %w", err)
			}
		}
	} else if !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return fmt.Errorf("open service: %w", err)
	}

	if !sameFile(selfPath, dst) {
		if err := copyExe(selfPath, dst); err != nil {
			return err
		}
	}

	args := []string{"service", "run", "--allow-sid", userSID}
	if exists {
		cfg, err := s.Config()
		if err != nil {
			return fmt.Errorf("read service config: %w", err)
		}
		cfg.BinaryPathName = quoteCmd(dst, args)
		cfg.StartType = mgr.StartAutomatic
		cfg.DisplayName = displayName
		cfg.Description = description
		if err := s.UpdateConfig(cfg); err != nil {
			return fmt.Errorf("update service: %w", err)
		}
	} else {
		s, err = m.CreateService(serviceName, dst, mgr.Config{
			StartType:   mgr.StartAutomatic,
			DisplayName: displayName,
			Description: description,
		}, args...)
		if err != nil {
			return fmt.Errorf("create service: %w", err)
		}
		defer s.Close()
	}

	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("start service: %w", err)
	}
	if err := waitState(s, winsvc.Running, 10*time.Second); err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

func quoteCmd(exe string, args []string) string {
	cmd := windows.EscapeArg(exe)
	for _, a := range args {
		cmd += " " + windows.EscapeArg(a)
	}
	return cmd
}

// Uninstall stops and deletes the wgent service and removes its install dir.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager (run as administrator): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err == nil {
		defer s.Close()
		if st, qerr := s.Query(); qerr == nil && st.State != winsvc.Stopped {
			if _, err := s.Control(winsvc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
				return fmt.Errorf("stop service: %w", err)
			}
			if err := waitState(s, winsvc.Stopped, 20*time.Second); err != nil {
				return fmt.Errorf("stop service: %w", err)
			}
		}
		if err := s.Delete(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
			return fmt.Errorf("delete service: %w", err)
		}
	} else if !errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return fmt.Errorf("open service: %w", err)
	}
	if err := os.RemoveAll(installDir()); err != nil {
		return fmt.Errorf("remove %s: %w", installDir(), err)
	}
	return nil
}

// Run runs the wgent service under the SCM, serving the named pipe for userSID.
func Run(userSID string) error {
	return winsvc.Run(serviceName, &service{sid: userSID})
}

type service struct{ sid string }

func (sv *service) Execute(_ []string, req <-chan winsvc.ChangeRequest, status chan<- winsvc.Status) (bool, uint32) {
	status <- winsvc.Status{State: winsvc.StartPending}
	l, err := ipc.Listen(sv.sid)
	if err != nil {
		log.Printf("wgent: listen: %v", err)
		return false, 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &daemon.Handler{P: platform.NewWindows(), Now: time.Now, Sleep: time.Sleep}
	done := make(chan error, 1)
	go func() { done <- ipc.Serve(ctx, l, h) }()

	status <- winsvc.Status{State: winsvc.Running, Accepts: winsvc.AcceptStop | winsvc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case winsvc.Interrogate:
				status <- c.CurrentStatus
			case winsvc.Stop, winsvc.Shutdown:
				status <- winsvc.Status{State: winsvc.StopPending}
				cancel()
				l.Close()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
				}
				return false, 0
			}
		case err := <-done:
			log.Printf("wgent: serve ended: %v", err)
			return false, 1
		}
	}
}
