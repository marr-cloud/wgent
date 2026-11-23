// Package platform abstracts the host operations the daemon needs (service
// control, WireGuard CLI, configuration discovery). The real Windows
// implementation lives in windows.go; this file is pure so the daemon logic can
// be tested with a fake.
package platform

import "github.com/marr-cloud/wgent/internal/health"

// Platform is the set of privileged host operations used by the daemon.
// Callers must only pass names that appear in ConfigNames().
type Platform interface {
	// ConfigNames returns tunnel names from the configs dir, sorted and
	// deduplicated (.conf.dpapi and .conf).
	ConfigNames() ([]string, error)
	// ConfigPath prefers .conf.dpapi; returns os.ErrNotExist if none.
	ConfigPath(name string) (string, error)
	// ServiceState reports SvcMissing when the tunnel service doesn't exist
	// and, for SvcFailed, the service's exit code.
	ServiceState(name string) (health.ServiceState, uint32, error)
	StartService(name string) error
	// InstallTunnel runs wireguard.exe /installtunnelservice <confPath>.
	InstallTunnel(confPath string) error
	// UninstallTunnel runs wireguard.exe /uninstalltunnelservice <name>.
	UninstallTunnel(name string) error
	// Dump runs wg.exe show <name> dump.
	Dump(name string) (string, error)
}
