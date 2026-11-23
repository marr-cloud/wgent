// Package daemon implements the privileged request handler behind the wgent
// service. It is the security boundary: it only ever acts on tunnel names that
// pass tunnel.ValidateName AND exist in the configurations directory, and it
// never builds filesystem paths itself.
package daemon

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/marr-cloud/wgent/internal/api"
	"github.com/marr-cloud/wgent/internal/health"
	"github.com/marr-cloud/wgent/internal/platform"
	"github.com/marr-cloud/wgent/internal/tunnel"
)

const (
	defaultTimeout = 15 * time.Second
	defaultPoll    = 250 * time.Millisecond
	// handshakeWait bounds how long `up` waits, after the service is running,
	// for the first handshake. It never exceeds the overall Timeout budget.
	handshakeWait = 5 * time.Second
)

// Handler implements ipc.Handler. Now and Sleep are injectable for tests;
// zero values default to time.Now / time.Sleep, Timeout to 15s, Poll to 250ms.
type Handler struct {
	P       platform.Platform
	Now     func() time.Time
	Sleep   func(time.Duration)
	Timeout time.Duration
	Poll    time.Duration
}

func (h *Handler) timeout() time.Duration {
	if h.Timeout > 0 {
		return h.Timeout
	}
	return defaultTimeout
}

func (h *Handler) poll() time.Duration {
	if h.Poll > 0 {
		return h.Poll
	}
	return defaultPoll
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) sleep(d time.Duration) {
	if h.Sleep != nil {
		h.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Handle executes one request. It never panics on unknown input and always
// sets ProtocolVersion on the response.
func (h *Handler) Handle(ctx context.Context, req api.Request) api.Response {
	resp := h.handle(ctx, req)
	resp.ProtocolVersion = api.ProtocolVersion
	return resp
}

func errResp(code, msg, hint string) api.Response {
	return api.Response{Error: &api.Error{Code: code, Message: msg, Hint: hint}}
}

func internalErr(err error) api.Response { return errResp(api.CodeInternal, err.Error(), "") }

func (h *Handler) handle(ctx context.Context, req api.Request) api.Response {
	switch req.Op {
	case api.OpList:
		return h.list()
	case api.OpStatus, api.OpUp, api.OpDown:
	default:
		return errResp(api.CodeUsage, fmt.Sprintf("unknown op %q", string(req.Op)), "")
	}

	name := req.Tunnel
	if err := tunnel.ValidateName(name); err != nil {
		return errResp(api.CodeInvalidName, err.Error(), "")
	}
	// ValidateName also accepts ".", ".." and "CON"; membership in the
	// configs directory is the real gate before any platform call that
	// takes the name.
	names, err := h.P.ConfigNames()
	if err != nil {
		return internalErr(err)
	}
	if !slices.Contains(names, name) {
		return errResp(api.CodeTunnelNotFound, fmt.Sprintf("tunnel %q not found", name), `run "wgent list"`)
	}

	switch req.Op {
	case api.OpStatus:
		st, err := h.status(name)
		if err != nil {
			return internalErr(err)
		}
		return api.Response{Status: &st}
	case api.OpUp:
		return h.up(ctx, name)
	default:
		return h.down(ctx, name)
	}
}

func (h *Handler) list() api.Response {
	names, err := h.P.ConfigNames()
	if err != nil {
		return internalErr(err)
	}
	out := make([]api.Status, 0, len(names))
	for _, n := range names {
		st, err := h.status(n)
		if err != nil {
			st = api.Status{Tunnel: n, State: health.StateError, Reason: err.Error(), Peers: []api.PeerInfo{}}
		}
		out = append(out, st)
	}
	return api.Response{Tunnels: out}
}

// status builds the Status for a name already known to exist in the configs.
func (h *Handler) status(name string) (api.Status, error) {
	svc, err := h.P.ServiceState(name)
	if err != nil {
		return api.Status{}, err
	}
	now := h.now()
	st := api.Status{Tunnel: name, Service: svc, Peers: []api.PeerInfo{}}
	var peers []health.Peer
	var statsErr error
	if svc == health.SvcRunning {
		var dump string
		if dump, statsErr = h.P.Dump(name); statsErr == nil {
			peers, statsErr = health.ParseDump(dump)
		}
	}
	st.State, st.Reason = health.Verdict(svc, peers, statsErr, now)
	for _, p := range peers {
		pi := api.PeerInfo{Endpoint: p.Endpoint, RxBytes: p.RxBytes, TxBytes: p.TxBytes}
		if !p.LastHandshake.IsZero() {
			hs := p.LastHandshake
			age := int64(now.Sub(hs) / time.Second)
			pi.LastHandshake, pi.HandshakeAgeS = &hs, &age
		}
		st.Peers = append(st.Peers, pi)
	}
	return st, nil
}

// ctxResp maps a context end to a response. Cancellation and deadline are both
// reported as CodeTimeout: the client's wait was cut short either way.
func ctxResp(name, what string) api.Response {
	return errResp(api.CodeTimeout, fmt.Sprintf("cancelled while waiting for tunnel %q to %s", name, what), "")
}

// waitService polls ServiceState until pred holds; it returns nil on success
// and an error response otherwise.
func (h *Handler) waitService(ctx context.Context, name, what string, deadline time.Time, pred func(health.ServiceState) bool) *api.Response {
	for {
		if ctx.Err() != nil {
			r := ctxResp(name, what)
			return &r
		}
		s, err := h.P.ServiceState(name)
		if err != nil {
			r := internalErr(err)
			return &r
		}
		if pred(s) {
			return nil
		}
		if !h.now().Before(deadline) {
			r := errResp(api.CodeTimeout, fmt.Sprintf("timed out waiting for tunnel %q to %s", name, what), "")
			return &r
		}
		h.sleep(h.poll())
	}
}

func (h *Handler) up(ctx context.Context, name string) api.Response {
	svc, err := h.P.ServiceState(name)
	if err != nil {
		return internalErr(err)
	}
	if svc == health.SvcRunning {
		return h.statusResp(name, false)
	}
	deadline := h.now().Add(h.timeout())
	switch svc {
	case health.SvcMissing:
		path, err := h.P.ConfigPath(name) // never construct paths ourselves
		if err != nil {
			return internalErr(err)
		}
		err = h.P.InstallTunnel(path)
		if err != nil {
			return internalErr(err)
		}
	case health.SvcStopped:
		if err := h.P.StartService(name); err != nil {
			return internalErr(err)
		}
	}
	// starting/stopping: just wait.
	if r := h.waitService(ctx, name, "start", deadline, func(s health.ServiceState) bool { return s == health.SvcRunning }); r != nil {
		return *r
	}

	// Service is running; give the first handshake a short window. Whatever
	// the verdict is at the end is returned (never an error for "no handshake").
	hsDeadline := h.now().Add(handshakeWait)
	if deadline.Before(hsDeadline) {
		hsDeadline = deadline
	}
	for {
		st, err := h.status(name)
		if err != nil {
			return internalErr(err)
		}
		if st.State == health.StateUp || !h.now().Before(hsDeadline) {
			st.Changed = true
			return api.Response{Status: &st}
		}
		if ctx.Err() != nil {
			return ctxResp(name, "complete a handshake")
		}
		h.sleep(h.poll())
	}
}

func (h *Handler) down(ctx context.Context, name string) api.Response {
	svc, err := h.P.ServiceState(name)
	if err != nil {
		return internalErr(err)
	}
	if svc == health.SvcMissing {
		return h.statusResp(name, false)
	}
	deadline := h.now().Add(h.timeout())
	if err := h.P.UninstallTunnel(name); err != nil {
		return internalErr(err)
	}
	if r := h.waitService(ctx, name, "stop", deadline, func(s health.ServiceState) bool { return s == health.SvcMissing }); r != nil {
		return *r
	}
	return h.statusResp(name, true)
}

func (h *Handler) statusResp(name string, changed bool) api.Response {
	st, err := h.status(name)
	if err != nil {
		return internalErr(err)
	}
	st.Changed = changed
	return api.Response{Status: &st}
}
