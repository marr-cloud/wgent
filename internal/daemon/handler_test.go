package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marr-cloud/wgent/internal/api"
	"github.com/marr-cloud/wgent/internal/health"
)

var t0 = time.Unix(1_800_000_000, 0)

type fake struct {
	names   []string
	state   map[string]health.ServiceState
	calls   []string
	now     time.Time
	dump    func(name string, n int) (string, error)
	dumpN   int
	onStart func(f *fake, name string) // default: becomes running
	onInst  func(f *fake, name string)
	onUnin  func(f *fake, name string)
}

func newFake(names ...string) *fake {
	return &fake{names: names, state: map[string]health.ServiceState{}, now: t0}
}

func (f *fake) rec(s string) { f.calls = append(f.calls, s) }

func (f *fake) mutating() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "start:") || strings.HasPrefix(c, "install:") || strings.HasPrefix(c, "uninstall:") {
			out = append(out, c)
		}
	}
	return out
}

func (f *fake) ConfigNames() ([]string, error) { f.rec("names"); return f.names, nil }
func (f *fake) ConfigPath(n string) (string, error) {
	f.rec("path:" + n)
	if !slices.Contains(f.names, n) {
		return "", os.ErrNotExist
	}
	return `C:\cfg\` + n + ".conf.dpapi", nil
}
func (f *fake) ServiceState(n string) (health.ServiceState, error) {
	f.rec("svc:" + n)
	if s, ok := f.state[n]; ok {
		return s, nil
	}
	return health.SvcMissing, nil
}
func (f *fake) StartService(n string) error {
	f.rec("start:" + n)
	if f.onStart != nil {
		f.onStart(f, n)
	} else {
		f.state[n] = health.SvcRunning
	}
	return nil
}
func (f *fake) InstallTunnel(p string) error {
	f.rec("install:" + p)
	n := strings.TrimSuffix(strings.TrimPrefix(p, `C:\cfg\`), ".conf.dpapi")
	if f.onInst != nil {
		f.onInst(f, n)
	} else {
		f.state[n] = health.SvcRunning
	}
	return nil
}
func (f *fake) UninstallTunnel(n string) error {
	f.rec("uninstall:" + n)
	if f.onUnin != nil {
		f.onUnin(f, n)
	} else {
		delete(f.state, n)
	}
	return nil
}
func (f *fake) Dump(n string) (string, error) {
	f.rec("dump:" + n)
	f.dumpN++
	if f.dump == nil {
		return dumpWithHandshake(f.now, 30), nil
	}
	return f.dump(n, f.dumpN)
}

func dumpWithHandshake(now time.Time, agoS int64) string {
	hs := int64(0)
	if agoS >= 0 {
		hs = now.Unix() - agoS
	}
	return fmt.Sprintf("PRIV\tPUB\t51820\toff\npeer=\t(none)\t203.0.113.5:51820\t10.0.0.2/32\t%d\t10\t20\t25\n", hs)
}

func (f *fake) handler() *Handler {
	return &Handler{
		P:     f,
		Now:   func() time.Time { return f.now },
		Sleep: func(d time.Duration) { f.now = f.now.Add(d) },
	}
}

func do(h *Handler, op api.Op, tunnel string) api.Response {
	return h.Handle(context.Background(), api.Request{ProtocolVersion: 1, Op: op, Tunnel: tunnel})
}

func wantErr(t *testing.T, r api.Response, code string) {
	t.Helper()
	if r.ProtocolVersion != api.ProtocolVersion {
		t.Errorf("protocol version = %d", r.ProtocolVersion)
	}
	if r.Error == nil || r.Error.Code != code {
		t.Fatalf("error = %+v, want code %s", r.Error, code)
	}
}

func wantOK(t *testing.T, r api.Response) *api.Status {
	t.Helper()
	if r.ProtocolVersion != api.ProtocolVersion {
		t.Errorf("protocol version = %d", r.ProtocolVersion)
	}
	if r.Error != nil || r.Status == nil {
		t.Fatalf("response = %+v", r)
	}
	return r.Status
}

func TestHandlerRejectsInvalidName(t *testing.T) {
	f := newFake("a")
	r := do(f.handler(), api.OpUp, `..\evil`)
	wantErr(t, r, api.CodeInvalidName)
	if len(f.calls) != 0 {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestHandlerUnknownTunnel(t *testing.T) {
	f := newFake("a-ado-cloud")
	r := do(f.handler(), api.OpStatus, "nope")
	wantErr(t, r, api.CodeTunnelNotFound)
	if r.Error.Hint != `run "wgent list"` {
		t.Errorf("hint = %q", r.Error.Hint)
	}
}

// ValidateName accepts ".", ".." and "CON"; membership must still gate them.
func TestHandlerNameAcceptedByValidatorButUnknownMakesNoPlatformCalls(t *testing.T) {
	for _, op := range []api.Op{api.OpUp, api.OpDown, api.OpStatus} {
		for _, n := range []string{".", "..", "CON", "nope"} {
			f := newFake("a")
			wantErr(t, do(f.handler(), op, n), api.CodeTunnelNotFound)
			for _, c := range f.calls {
				if c != "names" {
					t.Errorf("%s %q: unexpected platform call %s", op, n, c)
				}
			}
		}
	}
}

func TestHandlerUnknownOp(t *testing.T) {
	f := newFake("a")
	r := do(f.handler(), api.Op("rm"), "a")
	wantErr(t, r, api.CodeUsage)
	if !strings.Contains(r.Error.Message, "unknown op") {
		t.Errorf("message = %q", r.Error.Message)
	}
	if len(f.calls) != 0 {
		t.Errorf("calls = %v", f.calls)
	}
}

func TestUpInstallsWhenMissing(t *testing.T) {
	f := newFake("a")
	st := wantOK(t, do(f.handler(), api.OpUp, "a"))
	if !slices.Contains(f.calls, `install:C:\cfg\a.conf.dpapi`) {
		t.Errorf("calls = %v", f.calls)
	}
	if !st.Changed || st.State != health.StateUp || st.Service != health.SvcRunning {
		t.Errorf("status = %+v", st)
	}
}

func TestUpStartsExistingStoppedService(t *testing.T) {
	f := newFake("a")
	f.state["a"] = health.SvcStopped
	st := wantOK(t, do(f.handler(), api.OpUp, "a"))
	if !slices.Equal(f.mutating(), []string{"start:a"}) {
		t.Errorf("mutating = %v", f.mutating())
	}
	if !st.Changed {
		t.Error("want Changed")
	}
}

func TestUpIdempotent(t *testing.T) {
	f := newFake("a")
	f.state["a"] = health.SvcRunning
	st := wantOK(t, do(f.handler(), api.OpUp, "a"))
	if len(f.mutating()) != 0 || st.Changed || st.State != health.StateUp {
		t.Errorf("mutating=%v status=%+v", f.mutating(), st)
	}
}

func TestUpTimeout(t *testing.T) {
	f := newFake("a")
	f.onInst = func(*fake, string) {} // stays missing
	r := do(f.handler(), api.OpUp, "a")
	wantErr(t, r, api.CodeTimeout)
	if r.Error.Message != `timed out waiting for tunnel "a" to start` {
		t.Errorf("message = %q", r.Error.Message)
	}
	if got := f.now.Sub(t0); got < 15*time.Second || got > 16*time.Second {
		t.Errorf("fake time elapsed = %v", got)
	}
}

func TestUpWaitsForHandshake(t *testing.T) {
	f := newFake("a")
	f.dump = func(n string, c int) (string, error) {
		if c < 4 {
			return dumpWithHandshake(f.now, -1), nil
		}
		return dumpWithHandshake(f.now, 1), nil
	}
	st := wantOK(t, do(f.handler(), api.OpUp, "a"))
	if st.State != health.StateUp || !st.Changed {
		t.Errorf("status = %+v", st)
	}
}

func TestUpHandshakeNeverComesIsDegradedNotError(t *testing.T) {
	f := newFake("a")
	f.dump = func(string, int) (string, error) { return dumpWithHandshake(f.now, -1), nil }
	st := wantOK(t, do(f.handler(), api.OpUp, "a"))
	if st.State != health.StateDegraded || !st.Changed {
		t.Errorf("status = %+v", st)
	}
	if got := f.now.Sub(t0); got < 5*time.Second || got > 6*time.Second {
		t.Errorf("fake time elapsed = %v, want ~5s", got)
	}
}

func TestUpContextCancelled(t *testing.T) {
	f := newFake("a")
	f.onInst = func(*fake, string) {}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := f.handler().Handle(ctx, api.Request{Op: api.OpUp, Tunnel: "a"})
	wantErr(t, r, api.CodeTimeout)
}

func TestDownUninstalls(t *testing.T) {
	f := newFake("a")
	f.state["a"] = health.SvcRunning
	st := wantOK(t, do(f.handler(), api.OpDown, "a"))
	if !slices.Equal(f.mutating(), []string{"uninstall:a"}) || !st.Changed || st.State != health.StateDown {
		t.Errorf("mutating=%v status=%+v", f.mutating(), st)
	}
}

func TestDownIdempotent(t *testing.T) {
	f := newFake("a")
	st := wantOK(t, do(f.handler(), api.OpDown, "a"))
	if len(f.mutating()) != 0 || st.Changed || st.State != health.StateDown {
		t.Errorf("mutating=%v status=%+v", f.mutating(), st)
	}
}

func TestDownTimeout(t *testing.T) {
	f := newFake("a")
	f.state["a"] = health.SvcRunning
	f.onUnin = func(*fake, string) {}
	wantErr(t, do(f.handler(), api.OpDown, "a"), api.CodeTimeout)
}

func TestStatusRunningWithPeers(t *testing.T) {
	f := newFake("a")
	f.state["a"] = health.SvcRunning
	st := wantOK(t, do(f.handler(), api.OpStatus, "a"))
	if st.State != health.StateUp || st.Changed || len(st.Peers) != 1 {
		t.Fatalf("status = %+v", st)
	}
	p := st.Peers[0]
	if p.HandshakeAgeS == nil || *p.HandshakeAgeS != 30 || p.LastHandshake == nil || p.RxBytes != 10 || p.TxBytes != 20 {
		t.Errorf("peer = %+v", p)
	}
}

func TestStatusNeverHandshook(t *testing.T) {
	f := newFake("a")
	f.state["a"] = health.SvcRunning
	f.dump = func(string, int) (string, error) { return dumpWithHandshake(f.now, -1), nil }
	st := wantOK(t, do(f.handler(), api.OpStatus, "a"))
	p := st.Peers[0]
	if p.HandshakeAgeS != nil || p.LastHandshake != nil || st.State != health.StateDegraded {
		t.Errorf("status = %+v", st)
	}
}

func TestStatusDumpError(t *testing.T) {
	f := newFake("a")
	f.state["a"] = health.SvcRunning
	f.dump = func(string, int) (string, error) { return "", errors.New("boom") }
	st := wantOK(t, do(f.handler(), api.OpStatus, "a"))
	if st.State != health.StateError || !strings.Contains(st.Reason, "boom") {
		t.Errorf("status = %+v", st)
	}
}

func TestStatusStopped(t *testing.T) {
	f := newFake("a")
	f.state["a"] = health.SvcStopped
	st := wantOK(t, do(f.handler(), api.OpStatus, "a"))
	if st.State != health.StateDown || st.Service != health.SvcStopped {
		t.Errorf("status = %+v", st)
	}
	if slices.Contains(f.calls, "dump:a") {
		t.Error("dump should not be called when not running")
	}
}

func TestList(t *testing.T) {
	f := newFake("a", "b")
	f.state["a"] = health.SvcRunning
	r := do(f.handler(), api.OpList, "")
	if r.Error != nil || len(r.Tunnels) != 2 || r.ProtocolVersion != api.ProtocolVersion {
		t.Fatalf("response = %+v", r)
	}
	if r.Tunnels[0].Tunnel != "a" || r.Tunnels[0].State != health.StateUp ||
		r.Tunnels[1].Tunnel != "b" || r.Tunnels[1].State != health.StateDown {
		t.Errorf("tunnels = %+v", r.Tunnels)
	}
}

func TestDefaults(t *testing.T) {
	h := &Handler{P: newFake("a")}
	if h.timeout() != 15*time.Second || h.poll() != 250*time.Millisecond {
		t.Error("bad defaults")
	}
}
