package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/marr-cloud/wgent/internal/api"
	"github.com/marr-cloud/wgent/internal/health"
)

type fakeClient struct {
	reqs []api.Request
	fn   func(api.Request) (api.Response, error)
}

func (f *fakeClient) Do(r api.Request) (api.Response, error) {
	f.reqs = append(f.reqs, r)
	return f.fn(r)
}

type fakeSvc struct {
	elevated  bool
	installed bool
	ranSID    string
	uninstErr error
}

func (s *fakeSvc) Install() error       { s.installed = true; return nil }
func (s *fakeSvc) Uninstall() error     { return s.uninstErr }
func (s *fakeSvc) Run(sid string) error { s.ranSID = sid; return nil }
func (s *fakeSvc) IsElevated() bool     { return s.elevated }

type fakeTray struct{ calls []string }

func (t *fakeTray) Spawn() error         { t.calls = append(t.calls, "spawn"); return nil }
func (t *fakeTray) RunForeground() error { t.calls = append(t.calls, "fg"); return nil }
func (t *fakeTray) SetAutostart(on bool) error {
	if on {
		t.calls = append(t.calls, "auto-on")
	} else {
		t.calls = append(t.calls, "auto-off")
	}
	return nil
}

type env struct {
	out, errb bytes.Buffer
	client    *fakeClient
	svc       *fakeSvc
	tray      *fakeTray
	cfg       string
}

func statusResp(name string, st health.State) api.Response {
	return api.Response{Status: &api.Status{Tunnel: name, State: st, Changed: true}}
}

func setup(t *testing.T, defaultTunnel string) *env {
	t.Helper()
	e := &env{svc: &fakeSvc{}, tray: &fakeTray{}}
	e.cfg = filepath.Join(t.TempDir(), "wgent", "config.toml")
	if defaultTunnel != "" {
		if err := os.MkdirAll(filepath.Dir(e.cfg), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.cfg, []byte("default_tunnel = \""+defaultTunnel+"\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e.client = &fakeClient{fn: func(r api.Request) (api.Response, error) {
		return statusResp(r.Tunnel, health.StateUp), nil
	}}
	return e
}

func (e *env) run(args ...string) int {
	return Main(args, Deps{Client: e.client, ConfigPath: e.cfg, Stdout: &e.out, Stderr: &e.errb,
		Service: e.svc, Tray: e.tray, Version: "1.2.3"})
}

func TestStatusUsesDefault(t *testing.T) {
	e := setup(t, "work")
	if code := e.run("status"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errb.String())
	}
	if len(e.client.reqs) != 1 || e.client.reqs[0].Tunnel != "work" || e.client.reqs[0].Op != api.OpStatus {
		t.Fatalf("reqs = %+v", e.client.reqs)
	}
	if got := e.out.String(); got != "work: up\n" {
		t.Fatalf("out = %q", got)
	}
}

func TestNoDefaultExit2(t *testing.T) {
	e := setup(t, "")
	if code := e.run("up"); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(e.errb.String(), "wgent config set-default") {
		t.Fatalf("stderr = %q", e.errb.String())
	}
	if len(e.client.reqs) != 0 {
		t.Fatal("client should not be called")
	}
}

func TestInvalidNameExit2(t *testing.T) {
	e := setup(t, "")
	if code := e.run("up", "bad/name", "--json"); code != 2 {
		t.Fatalf("exit %d", code)
	}
	var v struct{ Error api.Error }
	if err := json.Unmarshal(e.out.Bytes(), &v); err != nil || v.Error.Code != api.CodeInvalidName {
		t.Fatalf("out = %q err=%v", e.out.String(), err)
	}
}

func TestStatusJSON(t *testing.T) {
	e := setup(t, "w")
	if code := e.run("--json", "status"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var st api.Status
	if err := json.Unmarshal(e.out.Bytes(), &st); err != nil || st.State != "up" {
		t.Fatalf("out = %q err=%v", e.out.String(), err)
	}
}

func TestServiceNotInstalledJSON(t *testing.T) {
	e := setup(t, "w")
	e.client.fn = func(api.Request) (api.Response, error) {
		return api.Response{}, &api.Error{Code: api.CodeServiceNotInstalled, Message: "wgent service is not running", Hint: "sudo wgent service install"}
	}
	if code := e.run("status", "--json"); code != 3 {
		t.Fatalf("exit %d", code)
	}
	var v struct{ Error api.Error }
	if err := json.Unmarshal(e.out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Error.Code != "service_not_installed" || !strings.Contains(v.Error.Hint, "sudo wgent service install") {
		t.Fatalf("out = %q", e.out.String())
	}
	if e.errb.Len() != 0 {
		t.Fatalf("stderr should be empty in json mode: %q", e.errb.String())
	}
}

func errCode(t *testing.T, code string, want int) {
	t.Helper()
	e := setup(t, "w")
	e.client.fn = func(api.Request) (api.Response, error) {
		return api.Response{}, &api.Error{Code: code, Message: "boom", Hint: "try this"}
	}
	if got := e.run("down"); got != want {
		t.Fatalf("%s: exit %d, want %d", code, got, want)
	}
	if e.errb.String() != "error: boom\nhint: try this\n" {
		t.Fatalf("stderr = %q", e.errb.String())
	}
}

func TestTunnelNotFoundExit4(t *testing.T) { errCode(t, api.CodeTunnelNotFound, 4) }
func TestTimeoutExit5(t *testing.T)        { errCode(t, api.CodeTimeout, 5) }
func TestProtocolMismatchExit3(t *testing.T) {
	errCode(t, api.CodeProtocolMismatch, 3)
}

func TestNonAPIErrorIsInternal(t *testing.T) {
	e := setup(t, "w")
	e.client.fn = func(api.Request) (api.Response, error) { return api.Response{}, errors.New("pipe broke") }
	if code := e.run("status", "--json"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	var v struct{ Error api.Error }
	if err := json.Unmarshal(e.out.Bytes(), &v); err != nil || v.Error.Code != "internal" || v.Error.Message != "pipe broke" {
		t.Fatalf("out = %q", e.out.String())
	}
}

func TestToggleUpWhenDown(t *testing.T) {
	e := setup(t, "w")
	e.client.fn = func(r api.Request) (api.Response, error) {
		if r.Op == api.OpStatus {
			return statusResp("w", health.StateDown), nil
		}
		return statusResp("w", health.StateUp), nil
	}
	if code := e.run("toggle"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(e.client.reqs) != 2 || e.client.reqs[1].Op != api.OpUp {
		t.Fatalf("reqs = %+v", e.client.reqs)
	}
}

func TestToggleDownWhenUp(t *testing.T) {
	e := setup(t, "w")
	e.client.fn = func(r api.Request) (api.Response, error) {
		if r.Op == api.OpDown {
			return statusResp("w", health.StateDown), nil
		}
		return statusResp("w", health.StateUp), nil
	}
	if code := e.run("toggle"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(e.client.reqs) != 2 || e.client.reqs[1].Op != api.OpDown {
		t.Fatalf("reqs = %+v", e.client.reqs)
	}
}

func TestUpNoChangeHuman(t *testing.T) {
	e := setup(t, "w")
	e.client.fn = func(api.Request) (api.Response, error) {
		return api.Response{Status: &api.Status{Tunnel: "w", State: health.StateUp, Changed: false}}, nil
	}
	e.run("up")
	if e.out.String() != "w: up (no change)\n" {
		t.Fatalf("out = %q", e.out.String())
	}
}

func TestStatusHumanHandshakeAndReason(t *testing.T) {
	e := setup(t, "w")
	age := int64(12)
	e.client.fn = func(api.Request) (api.Response, error) {
		return api.Response{Status: &api.Status{Tunnel: "work", State: health.StateDegraded, Reason: "handshake stale",
			Peers: []api.PeerInfo{{HandshakeAgeS: &age, RxBytes: 1229, TxBytes: 5632}}}}, nil
	}
	e.run("status")
	want := "work: degraded (handshake 12s ago, rx 1.2 KiB, tx 5.5 KiB) — handshake stale\n"
	if e.out.String() != want {
		t.Fatalf("out = %q", e.out.String())
	}
}

func TestListMarksDefault(t *testing.T) {
	e := setup(t, "b")
	e.client.fn = func(api.Request) (api.Response, error) {
		return api.Response{Tunnels: []api.Status{{Tunnel: "a", State: health.StateDown}, {Tunnel: "b", State: health.StateUp}}}, nil
	}
	if code := e.run("list"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if e.out.String() != "  a: down\n* b: up\n" {
		t.Fatalf("out = %q", e.out.String())
	}
}

func TestListJSON(t *testing.T) {
	e := setup(t, "b")
	e.client.fn = func(api.Request) (api.Response, error) {
		return api.Response{Tunnels: []api.Status{{Tunnel: "a", State: health.StateDown}, {Tunnel: "b", State: health.StateUp}}}, nil
	}
	e.run("list", "--json")
	var v struct {
		DefaultTunnel string       `json:"default_tunnel"`
		Tunnels       []api.Status `json:"tunnels"`
	}
	if err := json.Unmarshal(e.out.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.DefaultTunnel != "b" || len(v.Tunnels) != 2 || v.Tunnels[0].Default || !v.Tunnels[1].Default {
		t.Fatalf("out = %q", e.out.String())
	}
}

func TestListJSONEmptyIsArray(t *testing.T) {
	e := setup(t, "")
	e.client.fn = func(api.Request) (api.Response, error) { return api.Response{}, nil }
	e.run("list", "--json")
	if !strings.Contains(e.out.String(), `"tunnels":[]`) || !strings.Contains(e.out.String(), `"default_tunnel":""`) {
		t.Fatalf("out = %q", e.out.String())
	}
}

func TestConfigSetDefault(t *testing.T) {
	e := setup(t, "")
	if code := e.run("config", "set-default", "work"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.errb.String())
	}
	e.out.Reset()
	if code := e.run("config"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(e.out.String(), "path: "+e.cfg) || !strings.Contains(e.out.String(), `default_tunnel = "work"`) {
		t.Fatalf("out = %q", e.out.String())
	}
}

func TestConfigNoFile(t *testing.T) {
	e := setup(t, "")
	e.run("config")
	if e.out.String() != "path: "+e.cfg+"\n(no config file)\n" {
		t.Fatalf("out = %q", e.out.String())
	}
}

func TestConfigSetDefaultInvalid(t *testing.T) {
	e := setup(t, "")
	if code := e.run("config", "set-default", "a b"); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if _, err := os.Stat(e.cfg); err == nil {
		t.Fatal("config must not be written")
	}
}

func TestServiceInstallRequiresElevation(t *testing.T) {
	e := setup(t, "")
	if code := e.run("service", "install"); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(e.errb.String(), "run elevated: sudo wgent service install") || e.svc.installed {
		t.Fatalf("stderr = %q", e.errb.String())
	}
	e.svc.elevated = true
	if code := e.run("service", "install"); code != 0 || !e.svc.installed {
		t.Fatalf("exit %d", code)
	}
}

func TestServiceUninstallDeferred(t *testing.T) {
	e := setup(t, "")
	e.svc.elevated = true
	e.svc.uninstErr = ErrRemovalDeferred
	if code := e.run("service", "uninstall"); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(e.out.String(), "wgent.exe will be deleted after reboot") {
		t.Fatalf("out = %q", e.out.String())
	}
}

func TestServiceRun(t *testing.T) {
	e := setup(t, "")
	if code := e.run("service", "run", "--allow-sid", "S-1-5-21-1"); code != 0 || e.svc.ranSID != "S-1-5-21-1" {
		t.Fatalf("exit %d sid %q", code, e.svc.ranSID)
	}
	if e.out.Len() != 0 || e.errb.Len() != 0 {
		t.Fatal("service run must be silent")
	}
	if code := e.run("service", "run"); code != 2 {
		t.Fatalf("missing sid: exit %d", code)
	}
}

func TestTray(t *testing.T) {
	e := setup(t, "")
	e.run("tray")
	e.run("tray", "--foreground")
	e.run("tray", "--autostart", "on")
	e.run("tray", "--autostart", "off")
	if got := strings.Join(e.tray.calls, ","); got != "spawn,fg,auto-on,auto-off" {
		t.Fatalf("calls = %s", got)
	}
	if code := e.run("tray", "--autostart", "maybe"); code != 2 {
		t.Fatalf("exit %d", code)
	}
}

func TestVersionHelpUsage(t *testing.T) {
	e := setup(t, "")
	if code := e.run("version"); code != 0 || e.out.String() != "wgent 1.2.3\n" {
		t.Fatalf("exit %d out %q", code, e.out.String())
	}
	e.out.Reset()
	if code := e.run(); code != 0 || !strings.Contains(e.out.String(), "Usage:") {
		t.Fatalf("exit %d out %q", code, e.out.String())
	}
	e.out.Reset()
	if code := e.run("help"); code != 0 || !strings.Contains(e.out.String(), "Usage:") {
		t.Fatalf("exit %d", code)
	}
}

func TestUnknownCommandAndFlag(t *testing.T) {
	e := setup(t, "w")
	if code := e.run("bogus"); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if code := e.run("status", "--wat"); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if code := e.run("status", "a", "b"); code != 2 {
		t.Fatalf("exit %d", code)
	}
	e.out.Reset()
	if code := e.run("bogus", "--json"); code != 2 || !strings.Contains(e.out.String(), `"code":"usage"`) {
		t.Fatalf("exit %d out %q", code, e.out.String())
	}
}

func TestFormatSize(t *testing.T) {
	cases := map[uint64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1229: "1.2 KiB", 1 << 20: "1.0 MiB", 3 << 30: "3.0 GiB", 5 << 40: "5120.0 GiB"}
	for in, want := range cases {
		if got := formatSize(in); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", in, got, want)
		}
	}
}

// list does not need the default tunnel, so a broken config must not fail it.
func TestListWithMalformedConfig(t *testing.T) {
	e := setup(t, "x")
	if err := os.WriteFile(e.cfg, []byte("default_tunnel = [broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.client.fn = func(api.Request) (api.Response, error) {
		return api.Response{Tunnels: []api.Status{{Tunnel: "a", State: health.StateDown}}}, nil
	}
	if code := e.run("list", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, e.out.String())
	}
	if !strings.Contains(e.out.String(), `"default_tunnel":""`) || !strings.Contains(e.errb.String(), "warning") {
		t.Fatalf("out = %q, err = %q", e.out.String(), e.errb.String())
	}
}

func TestResolveVersion(t *testing.T) {
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: v}}, true }
	}
	none := func() (*debug.BuildInfo, bool) { return nil, false }
	cases := []struct {
		ldflags string
		info    func() (*debug.BuildInfo, bool)
		want    string
	}{
		{"v0.1.0", info("v0.2.0"), "v0.1.0"}, // ldflags win
		{"dev", info("v0.1.0"), "v0.1.0"},    // go install module@version
		{"dev", info("(devel)"), "dev"},      // local go build
		{"dev", info(""), "dev"},
		{"dev", none, "dev"},
	}
	for _, c := range cases {
		if got := ResolveVersion(c.ldflags, c.info); got != c.want {
			t.Errorf("ResolveVersion(%q) = %q, want %q", c.ldflags, got, c.want)
		}
	}
}
