// Package cli implements the wgent command line. It is pure: all side effects
// go through Deps, so it builds and tests on any OS.
package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/marr-cloud/wgent/internal/api"
	"github.com/marr-cloud/wgent/internal/config"
	"github.com/marr-cloud/wgent/internal/health"
	"github.com/marr-cloud/wgent/internal/tunnel"
)

// ErrRemovalDeferred is returned (possibly wrapped) by ServiceOps.Uninstall when
// wgent.exe could not be deleted now and will be removed after reboot.
var ErrRemovalDeferred = errors.New("wgent.exe will be deleted after reboot")

type Deps struct {
	Client interface {
		Do(api.Request) (api.Response, error)
	}
	ConfigPath string
	Stdout     io.Writer
	Stderr     io.Writer
	Service    ServiceOps
	Tray       TrayOps
	Version    string
}

type ServiceOps interface {
	Install() error
	Uninstall() error
	Run(sid string) error
	IsElevated() bool
}

type TrayOps interface {
	Spawn() error
	RunForeground() error
	SetAutostart(on bool) error
}

const usageText = `wgent - WireGuard tunnel control for agents

Usage:
  wgent list [--json]
  wgent status [tunnel] [--json]
  wgent up [tunnel] [--json]
  wgent down [tunnel] [--json]
  wgent toggle [tunnel] [--json]
  wgent config
  wgent config set-default <tunnel>
  wgent tray [--autostart on|off] [--foreground]
  wgent service install|uninstall
  wgent version
  wgent help

If [tunnel] is omitted, default_tunnel from the config file is used.
Exit codes: 0 ok, 1 error, 2 usage/no default tunnel, 3 service not installed,
4 tunnel not found, 5 timeout.
`

type app struct {
	d    Deps
	json bool
}

// Main runs the CLI and returns the process exit code.
func Main(args []string, d Deps) int {
	a := &app{d: d}
	var rest []string
	for _, s := range args {
		if s == "--json" {
			a.json = true
		} else {
			rest = append(rest, s)
		}
	}
	if len(rest) == 0 {
		fmt.Fprint(d.Stdout, usageText)
		return 0
	}
	cmd, rest := rest[0], rest[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(d.Stdout, usageText)
		return 0
	case "version":
		fmt.Fprintf(d.Stdout, "wgent %s\n", d.Version)
		return 0
	case "list":
		if len(rest) > 0 {
			return a.usage("list takes no arguments")
		}
		return a.list()
	case "status":
		return a.tunnelCmd(rest, api.OpStatus)
	case "up":
		return a.tunnelCmd(rest, api.OpUp)
	case "down":
		return a.tunnelCmd(rest, api.OpDown)
	case "toggle":
		return a.tunnelCmd(rest, "toggle")
	case "config":
		return a.configCmd(rest)
	case "tray":
		return a.trayCmd(rest)
	case "service":
		return a.serviceCmd(rest)
	}
	return a.usage(fmt.Sprintf("unknown command %q", cmd))
}

func exitCode(code string) int {
	switch code {
	case api.CodeUsage, api.CodeNoDefault, api.CodeInvalidName:
		return 2
	case api.CodeServiceNotInstalled, api.CodeProtocolMismatch:
		return 3
	case api.CodeTunnelNotFound:
		return 4
	case api.CodeTimeout:
		return 5
	}
	return 1
}

// fail prints err (JSON on stdout, or human on stderr) and returns its exit code.
func (a *app) fail(err error) int {
	var ae *api.Error
	if !errors.As(err, &ae) {
		ae = &api.Error{Code: api.CodeInternal, Message: err.Error()}
	}
	if a.json {
		writeJSON(a.d.Stdout, struct {
			Error *api.Error `json:"error"`
		}{ae})
	} else {
		fmt.Fprintf(a.d.Stderr, "error: %s\n", ae.Message)
		if ae.Hint != "" {
			fmt.Fprintf(a.d.Stderr, "hint: %s\n", ae.Hint)
		}
	}
	return exitCode(ae.Code)
}

func (a *app) failCode(code, msg, hint string) int {
	return a.fail(&api.Error{Code: code, Message: msg, Hint: hint})
}

func (a *app) usage(msg string) int {
	return a.failCode(api.CodeUsage, msg, "run: wgent help")
}

// resolve turns the optional tunnel argument into a validated tunnel name.
func (a *app) resolve(arg string) (string, error) {
	def := ""
	if arg == "" {
		c, err := config.Load(a.d.ConfigPath)
		if err != nil {
			return "", err
		}
		def = c.DefaultTunnel
	}
	name, err := tunnel.Resolve(arg, def)
	switch {
	case errors.Is(err, tunnel.ErrNoDefault):
		return "", &api.Error{Code: api.CodeNoDefault, Message: err.Error(), Hint: "wgent config set-default <tunnel>"}
	case errors.Is(err, tunnel.ErrInvalidName):
		return "", &api.Error{Code: api.CodeInvalidName, Message: err.Error()}
	}
	return name, err
}

func (a *app) list() int {
	c, err := config.Load(a.d.ConfigPath)
	if err != nil {
		return a.fail(err)
	}
	resp, err := a.d.Client.Do(api.Request{Op: api.OpList})
	if err != nil {
		return a.fail(err)
	}
	tunnels := resp.Tunnels
	if tunnels == nil {
		tunnels = []api.Status{}
	}
	for i := range tunnels {
		tunnels[i].Default = c.DefaultTunnel != "" && tunnels[i].Tunnel == c.DefaultTunnel
	}
	if a.json {
		writeJSON(a.d.Stdout, struct {
			DefaultTunnel string       `json:"default_tunnel"`
			Tunnels       []api.Status `json:"tunnels"`
		}{c.DefaultTunnel, tunnels})
		return 0
	}
	for _, s := range tunnels {
		prefix := "  "
		if s.Default {
			prefix = "* "
		}
		fmt.Fprintln(a.d.Stdout, prefix+statusLine(s, false))
	}
	return 0
}

func (a *app) do(op api.Op, name string) (*api.Status, error) {
	resp, err := a.d.Client.Do(api.Request{Op: op, Tunnel: name})
	if err != nil {
		return nil, err
	}
	if resp.Status == nil {
		return nil, errors.New("service returned no status")
	}
	return resp.Status, nil
}

func (a *app) tunnelCmd(rest []string, op api.Op) int {
	arg := ""
	for _, s := range rest {
		switch {
		case strings.HasPrefix(s, "-"):
			return a.usage(fmt.Sprintf("unknown flag %q", s))
		case arg != "":
			return a.usage("too many arguments")
		default:
			arg = s
		}
	}
	name, err := a.resolve(arg)
	if err != nil {
		return a.fail(err)
	}
	if op == "toggle" {
		cur, err := a.do(api.OpStatus, name)
		if err != nil {
			return a.fail(err)
		}
		op = api.OpDown
		if cur.State == health.StateDown {
			op = api.OpUp
		}
	}
	st, err := a.do(op, name)
	if err != nil {
		return a.fail(err)
	}
	if a.json {
		writeJSON(a.d.Stdout, st)
	} else {
		fmt.Fprintln(a.d.Stdout, statusLine(*st, op == api.OpUp || op == api.OpDown))
	}
	return 0
}

func (a *app) configCmd(rest []string) int {
	switch {
	case len(rest) == 0:
		fmt.Fprintf(a.d.Stdout, "path: %s\n", a.d.ConfigPath)
		data, err := os.ReadFile(a.d.ConfigPath)
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintln(a.d.Stdout, "(no config file)")
			return 0
		}
		if err != nil {
			return a.fail(err)
		}
		fmt.Fprint(a.d.Stdout, string(data))
		return 0
	case rest[0] == "set-default" && len(rest) == 2 && !strings.HasPrefix(rest[1], "-"):
		if err := tunnel.ValidateName(rest[1]); err != nil {
			return a.failCode(api.CodeInvalidName, err.Error(), "")
		}
		if err := config.SetDefault(a.d.ConfigPath, rest[1]); err != nil {
			return a.fail(err)
		}
		fmt.Fprintf(a.d.Stdout, "default tunnel set to %s\n", rest[1])
		return 0
	}
	return a.usage("usage: wgent config [set-default <tunnel>]")
}

func (a *app) trayCmd(rest []string) int {
	foreground := false
	autostart := ""
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--foreground":
			foreground = true
		case "--autostart":
			if i+1 >= len(rest) {
				return a.usage("--autostart needs on or off")
			}
			i++
			autostart = rest[i]
		default:
			return a.usage(fmt.Sprintf("unknown argument %q", rest[i]))
		}
	}
	var err error
	switch {
	case autostart != "":
		if autostart != "on" && autostart != "off" {
			return a.usage("--autostart needs on or off")
		}
		err = a.d.Tray.SetAutostart(autostart == "on")
	case foreground:
		err = a.d.Tray.RunForeground()
	default:
		err = a.d.Tray.Spawn()
	}
	if err != nil {
		return a.fail(err)
	}
	return 0
}

func (a *app) serviceCmd(rest []string) int {
	if len(rest) == 0 {
		return a.usage("usage: wgent service install|uninstall")
	}
	switch rest[0] {
	case "install", "uninstall":
		if len(rest) != 1 {
			return a.usage("too many arguments")
		}
		if !a.d.Service.IsElevated() {
			return a.failCode(api.CodeUsage, "run elevated: sudo wgent service "+rest[0], "")
		}
		if rest[0] == "install" {
			if err := a.d.Service.Install(); err != nil {
				return a.fail(err)
			}
			fmt.Fprintln(a.d.Stdout, "wgent service installed and started")
			return 0
		}
		if err := a.d.Service.Uninstall(); err != nil {
			if errors.Is(err, ErrRemovalDeferred) {
				fmt.Fprintln(a.d.Stdout, "wgent service removed; wgent.exe will be deleted after reboot")
				return 0
			}
			return a.fail(err)
		}
		fmt.Fprintln(a.d.Stdout, "wgent service removed")
		return 0
	case "run":
		if len(rest) != 3 || rest[1] != "--allow-sid" || rest[2] == "" {
			return a.usage("usage: wgent service run --allow-sid <sid>")
		}
		if err := a.d.Service.Run(rest[2]); err != nil {
			return a.fail(err)
		}
		return 0
	}
	return a.usage(fmt.Sprintf("unknown service command %q", rest[0]))
}
