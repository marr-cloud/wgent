//go:build windows

package tray

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"fyne.io/systray"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/marr-cloud/wgent/internal/api"
	"github.com/marr-cloud/wgent/internal/config"
	"github.com/marr-cloud/wgent/internal/health"
	"github.com/marr-cloud/wgent/internal/ipc"
)

const (
	mutexName       = `Local\wgent-tray`
	createNoWindow  = 0x08000000
	detachedProcess = 0x00000008
	actionErrTTL    = 30 * time.Second
)

// Tray is the system tray icon. Client talks to the wgent service.
type Tray struct {
	Client     ipc.Client
	ConfigPath string

	mu        sync.Mutex
	busy      bool      // a toggle/up/down request is in flight
	actionErr string    // last action error, shown in the tooltip for a while
	actionAt  time.Time // when actionErr was set
	names     []string  // tunnel names the menu was built for
	menuGen   chan struct{}
	items     map[string]*systray.MenuItem
	kick      chan struct{}
	stop      chan struct{}
}

// RunForeground runs the tray until Quit. If another instance already holds
// the single-instance mutex it returns nil immediately.
func (t *Tray) RunForeground() error {
	name, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return err
	}
	h, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if h != 0 {
			windows.CloseHandle(h)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("create tray mutex: %w", err)
	}
	defer windows.CloseHandle(h)

	t.kick = make(chan struct{}, 1)
	t.stop = make(chan struct{})
	systray.Run(t.onReady, func() { close(t.stop) })
	return nil
}

func (t *Tray) onReady() {
	systray.SetIcon(DotICO(ColorDown))
	systray.SetTooltip("wgent")
	// Left click toggles the default tunnel; right click opens the menu.
	systray.SetOnTapped(func() {
		cfg, err := config.Load(t.ConfigPath)
		if err != nil || cfg.DefaultTunnel == "" {
			return
		}
		go t.toggle(cfg.DefaultTunnel)
	})
	go t.pollLoop()
}

func (t *Tray) pollLoop() {
	for {
		secs := t.poll()
		select {
		case <-t.stop:
			return
		case <-t.kick:
		case <-time.After(time.Duration(secs) * time.Second):
		}
	}
}

func (t *Tray) setView(v View) {
	systray.SetIcon(DotICO(v.Color))
	systray.SetTooltip(v.Tooltip)
}

// poll refreshes menu and icon; it returns the poll interval in seconds.
// The config is re-read every time so `wgent config set-default` applies live.
func (t *Tray) poll() int {
	cfg, cerr := config.Load(t.ConfigPath)
	secs := cfg.Tray.PollSeconds
	if secs <= 0 {
		secs = 2
	}
	if cerr != nil {
		t.mu.Lock()
		busy := t.busy
		t.mu.Unlock()
		if !busy {
			t.setView(View{ColorError, Truncate("wgent: "+cerr.Error(), MaxTooltip)})
		}
		return secs
	}

	list, lerr := t.Client.Do(api.Request{Op: api.OpList})
	if lerr == nil {
		t.syncMenu(list.Tunnels)
	}

	var st *api.Status
	var serr error = lerr
	if cfg.DefaultTunnel != "" && lerr == nil {
		var resp api.Response
		resp, serr = t.Client.Do(api.Request{Op: api.OpStatus, Tunnel: cfg.DefaultTunnel})
		st = resp.Status
	}
	v := ViewFor(cfg.DefaultTunnel, st, serr)

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.busy {
		return secs // keep the amber icon until the request returns
	}
	if t.actionErr != "" && time.Since(t.actionAt) < actionErrTTL {
		v.Tooltip = Truncate("wgent: "+t.actionErr, MaxTooltip)
	} else {
		t.actionErr = ""
	}
	t.setView(v)
	return secs
}

// syncMenu keeps one checkbox per tunnel. The menu is rebuilt (ResetMenu, then
// re-add everything including Quit) only when the set of names changes; on
// every poll only the checkmarks are updated. Old click-handler goroutines
// exit via the closed generation channel.
func (t *Tray) syncMenu(tunnels []api.Status) {
	names := make([]string, len(tunnels))
	for i, s := range tunnels {
		names[i] = s.Tunnel
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.items == nil || !SameNames(names, t.names) {
		if t.menuGen != nil {
			close(t.menuGen)
		}
		t.menuGen = make(chan struct{})
		systray.ResetMenu()
		t.items = make(map[string]*systray.MenuItem, len(names))
		for _, s := range tunnels {
			name := s.Tunnel
			it := systray.AddMenuItemCheckbox(name, "Toggle "+name, s.State != health.StateDown)
			t.items[name] = it
			go func(gen chan struct{}) {
				for {
					select {
					case <-gen:
						return
					case <-it.ClickedCh:
						go t.toggle(name)
					}
				}
			}(t.menuGen)
		}
		systray.AddSeparator()
		quit := systray.AddMenuItem("Quit", "Quit wgent tray")
		go func(gen chan struct{}) {
			select {
			case <-gen:
			case <-quit.ClickedCh:
				systray.Quit()
			}
		}(t.menuGen)
		t.names = names
		return
	}
	for _, s := range tunnels {
		if it := t.items[s.Tunnel]; it != nil {
			if s.State != health.StateDown {
				it.Check()
			} else {
				it.Uncheck()
			}
		}
	}
}

// toggle flips a tunnel. Further clicks are ignored while a request is in flight.
func (t *Tray) toggle(name string) {
	t.mu.Lock()
	if t.busy {
		t.mu.Unlock()
		return
	}
	t.busy = true
	t.actionErr = ""
	t.setView(View{ColorPending, Truncate("wgent: "+name+" changing…", MaxTooltip)})
	t.mu.Unlock()

	err := t.doToggle(name)

	t.mu.Lock()
	t.busy = false
	if err != nil {
		t.actionErr = err.Error()
		t.actionAt = time.Now()
		t.setView(View{ColorError, Truncate("wgent: "+t.actionErr, MaxTooltip)})
	}
	t.mu.Unlock()
	select {
	case t.kick <- struct{}{}:
	default:
	}
}

func (t *Tray) doToggle(name string) error {
	resp, err := t.Client.Do(api.Request{Op: api.OpStatus, Tunnel: name})
	if err != nil {
		return err
	}
	op := api.OpUp
	if resp.Status != nil && resp.Status.State != health.StateDown {
		op = api.OpDown
	}
	_, err = t.Client.Do(api.Request{Op: op, Tunnel: name})
	return err
}

// Spawn relaunches this executable as a detached, windowless tray process.
func (t *Tray) Spawn() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "tray", "--foreground")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | detachedProcess, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// SetAutostart adds or removes the HKCU Run entry. Removing a missing entry is not an error.
func (t *Tray) SetAutostart(on bool) error {
	if !on {
		k, err := registry.OpenKey(registry.CURRENT_USER, RunKeyPath, registry.SET_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer k.Close()
		if err := k.DeleteValue(AutostartValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, RunKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(AutostartValueName, AutostartCommand(root, exe))
}
