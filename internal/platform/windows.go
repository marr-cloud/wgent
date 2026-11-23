//go:build windows

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/marr-cloud/wgent/internal/health"
	"github.com/marr-cloud/wgent/internal/tunnel"
)

const createNoWindow = 0x08000000

// Windows is the real Platform: SCM, wireguard.exe, wg.exe and the WireGuard
// configurations directory.
type Windows struct {
	WireGuardDir string
	Run          func(name string, args ...string) ([]byte, error)
	dirErr       error // set by NewWindows when Program Files cannot be resolved
}

// ProgramFiles returns the absolute Program Files directory, preferring the
// known-folder API and falling back to %ProgramFiles%.
func ProgramFiles() (string, error) {
	return resolveProgramFiles(func() (string, error) {
		return windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	}, os.Getenv)
}

func resolveProgramFiles(known func() (string, error), getenv func(string) string) (string, error) {
	if p, err := known(); err == nil && filepath.IsAbs(p) {
		return p, nil
	}
	p := getenv("ProgramFiles")
	if p == "" {
		return "", errors.New("cannot determine the Program Files directory")
	}
	if !filepath.IsAbs(p) {
		return "", fmt.Errorf("%%ProgramFiles%% is not an absolute path: %q", p)
	}
	return p, nil
}

var _ Platform = (*Windows)(nil)

// NewWindows returns a Platform rooted at %ProgramFiles%\WireGuard.
func NewWindows() *Windows {
	pf, err := ProgramFiles()
	if err != nil {
		return &Windows{Run: runHidden, dirErr: err}
	}
	return &Windows{WireGuardDir: filepath.Join(pf, "WireGuard"), Run: runHidden}
}

func runHidden(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd.CombinedOutput()
}

func (w *Windows) configDir() string {
	return filepath.Join(w.WireGuardDir, "Data", "Configurations")
}

func (w *Windows) run(exe string, args ...string) ([]byte, error) {
	run := w.Run
	if run == nil {
		run = runHidden
	}
	if w.dirErr != nil {
		return nil, w.dirErr
	}
	out, err := run(filepath.Join(w.WireGuardDir, exe), args...)
	if err != nil {
		msg := fmt.Sprintf("%s %s: %v", exe, strings.Join(args, " "), err)
		if o := strings.TrimSpace(string(out)); o != "" {
			msg += ": " + o
		}
		return out, wrapped{msg, err}
	}
	return out, nil
}

func trimConfSuffix(file string) (string, bool) {
	lower := strings.ToLower(file)
	for _, suf := range []string{".conf.dpapi", ".conf"} {
		if strings.HasSuffix(lower, suf) {
			return file[:len(file)-len(suf)], true
		}
	}
	return "", false
}

func (w *Windows) ConfigNames() ([]string, error) {
	if w.dirErr != nil {
		return nil, w.dirErr
	}
	entries, err := os.ReadDir(w.configDir())
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	names := []string{}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		name, ok := trimConfSuffix(e.Name())
		if !ok || seen[name] || tunnel.ValidateName(name) != nil {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func (w *Windows) ConfigPath(name string) (string, error) {
	if w.dirErr != nil {
		return "", w.dirErr
	}
	for _, suf := range []string{".conf.dpapi", ".conf"} {
		p := filepath.Join(w.configDir(), name+suf)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", fmt.Errorf("config for tunnel %q: %w", name, os.ErrNotExist)
}

func tunnelServiceName(name string) string { return "WireGuardTunnel$" + name }

func (w *Windows) ServiceState(name string) (health.ServiceState, error) {
	m, err := mgr.Connect()
	if err != nil {
		return "", fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(tunnelServiceName(name))
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) || errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
			return health.SvcMissing, nil
		}
		return "", fmt.Errorf("open service: %w", err)
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return "", fmt.Errorf("query service: %w", err)
	}
	switch st.State {
	case svc.Stopped:
		return health.SvcStopped, nil
	case svc.StartPending, svc.ContinuePending:
		return health.SvcStarting, nil
	case svc.Running:
		return health.SvcRunning, nil
	default:
		return health.SvcStopping, nil
	}
}

func (w *Windows) StartService(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to service manager: %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(tunnelServiceName(name))
	if err != nil {
		return fmt.Errorf("open service: %w", err)
	}
	defer s.Close()
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("start service: %w", err)
	}
	return nil
}

func (w *Windows) InstallTunnel(confPath string) error {
	_, err := w.run("wireguard.exe", "/installtunnelservice", confPath)
	return err
}

func (w *Windows) UninstallTunnel(name string) error {
	_, err := w.run("wireguard.exe", "/uninstalltunnelservice", name)
	return err
}

func (w *Windows) Dump(name string) (string, error) {
	out, err := w.run("wg.exe", "show", name, "dump")
	if err != nil {
		return "", err
	}
	return string(out), nil
}

type wrapped struct {
	msg string
	err error
}

func (e wrapped) Error() string { return e.msg }
func (e wrapped) Unwrap() error { return e.err }
