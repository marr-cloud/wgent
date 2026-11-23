//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/marr-cloud/wgent/internal/cli"
	"github.com/marr-cloud/wgent/internal/config"
	"github.com/marr-cloud/wgent/internal/ipc"
	"github.com/marr-cloud/wgent/internal/svc"
)

// version is set with -ldflags "-X main.version=...".
var version = "dev"

type serviceOps struct{}

func (serviceOps) Install() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	sid, err := svc.CurrentUserSID()
	if err != nil {
		return err
	}
	return svc.Install(exe, sid)
}

func (serviceOps) Uninstall() error {
	err := svc.Uninstall()
	if errors.Is(err, svc.ErrRemovalDeferred) {
		return cli.ErrRemovalDeferred
	}
	return err
}

func (serviceOps) Run(sid string) error { return svc.Run(sid) }
func (serviceOps) IsElevated() bool     { return svc.IsElevated() }

// trayStub is replaced by the real tray implementation in a later task.
type trayStub struct{}

func (trayStub) Spawn() error         { return errors.New("tray not implemented yet") }
func (trayStub) RunForeground() error { return errors.New("tray not implemented yet") }
func (trayStub) SetAutostart(bool) error {
	return errors.New("tray not implemented yet")
}

func main() {
	cfgPath, err := config.Path(os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	os.Exit(cli.Main(os.Args[1:], cli.Deps{
		Client:     ipc.Client{Dial: ipc.Dial},
		ConfigPath: cfgPath,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Service:    serviceOps{},
		Tray:       trayStub{},
		Version:    version,
	}))
}
