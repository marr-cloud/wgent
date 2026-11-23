// Package daemon implements the privileged request handler behind the wgent
// service. It is the security boundary: it only ever acts on tunnel names that
// pass tunnel.ValidateName AND exist in the configurations directory, and it
// never builds filesystem paths itself.
package daemon

import (
	"context"
	"fmt"
	"slices"
	"sync"
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

	mu    sync.Mutex
	locks map[string]chan struct{} // per-tunnel lock for up/down
}

// lock serializes mutating operations on one tunnel. It gives up (ok=false)
// if ctx ends first.
func (h *Handler) lock(ctx context.Context, name string) (unlock func(), ok bool) {
	h.mu.Lock()
	if h.locks == nil {
		h.locks = map[string]chan struct{}{}
	}
	l, found := h.locks[name]
	if !found {
		l = make(chan struct{}, 1)
		h.locks[name] = l
	}
	h.mu.Unlock()
	select {
	case l <- struct{}{}:
		return func() { <-l }, true
	case <-ctx.Done():
		return nil, false
	}
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

	if req.Op == api.OpStatus {
		st, err := h.status(name)
		if err != nil {
			return internalErr(err)
		}
		return api.Response{Status: &st}
	}
	// up/down (and so toggle) are serialized per tunnel: a second concurrent
	// request sees the result of the first instead of racing it.
	what := "start"
	if req.Op == api.OpDown {
		what = "stop"
	}
	unlock, ok := h.lock(ctx, name)
	if !ok {
		return ctxResp(name, what)
	}
	defer unlock()
	if req.Op == api.OpUp {
		return h.up(ctx, name)
	}
	return h.down(ctx, name)
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
			st = api.Status{Tunnel: n, State: health.StateError, Reason: err.Error(), Service: health.SvcUnknown, Peers: []api.PeerInfo{}}
		}
		out = append(out, st)
	}
	return api.Response{Tunnels: out}
}

// status builds the Status for a name already known to exist in the configs.
func (h *Handler) status(name string) (api.Status, error) {
	svc, code, err := h.P.ServiceState(name)
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
	if svc == health.SvcFailed {
		st.Reason = health.FailedReason(code)
	}
	for _, p := range peers {
		pi := api.PeerInfo{Endpoint: p.Endpoint, RxBytes: p.RxBytes, TxBytes: p.TxBytes}
		if !p.LastHandshake.IsZero() {
			hs := p.LastHandshake
			age := max(int64(now.Sub(hs)/time.Second), 0) // clamp clock skew
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
// and an error response otherwise. fail, if set, may end the wait early with
// an error for states the service will not leave on its own.
func (h *Handler) waitService(ctx context.Context, name, what string, deadline time.Time, pred func(health.ServiceState) bool, fail func(health.ServiceState, uint32) *api.Response) *api.Response {
	for {
		if ctx.Err() != nil {
			r := ctxResp(name, what)
			return &r
		}
		s, code, err := h.P.ServiceState(name)
		if err != nil {
			r := internalErr(err)
			return &r
		}
		if pred(s) {
			return nil
		}
		if fail != nil {
			if r := fail(s, code); r != nil {
				return r
			}
		}
		if !h.now().Before(deadline) {
			r := errResp(api.CodeTimeout, fmt.Sprintf("timed out waiting for tunnel %q to %s", name, what), "")
			return &r
		}
		h.sleep(h.poll())
	}
}

// startFailed ends a start wait once the service stopped after we asked it to
// start: it will not come back on its own, so report the cause now.
func startFailed(name string) func(health.ServiceState, uint32) *api.Response {
	return func(s health.ServiceState, code uint32) *api.Response {
		var r api.Response
		switch s {
		case health.SvcFailed:
			r = errResp(api.CodeInternal, fmt.Sprintf("tunnel %q: %s", name, health.FailedReason(code)), "")
		case health.SvcStopped:
			r = errResp(api.CodeInternal, fmt.Sprintf("tunnel %q service stopped while starting; see the WireGuard log", name), "")
		default:
			return nil
		}
		return &r
	}
}

func pausedErr(name string) api.Response {
	return errResp(api.CodeInternal, fmt.Sprintf("tunnel %q service is paused; resume it or run \"wgent down %s\"", name, name), "")
}

func (h *Handler) up(ctx context.Context, name string) api.Response {
	svc, _, err := h.P.ServiceState(name)
	if err != nil {
		return internalErr(err)
	}
	if svc == health.SvcRunning {
		return h.statusResp(name, false)
	}
	if svc == health.SvcPaused {
		return pausedErr(name)
	}
	deadline := h.now().Add(h.timeout())
	if svc == health.SvcStarting || svc == health.SvcStopping {
		// Transition in progress: wait (same budget) for a settled state, then
		// decide what to do. A stopping service ends up stopped/missing and
		// must then be started/installed.
		if r := h.waitService(ctx, name, "start", deadline, func(s health.ServiceState) bool {
			return s != health.SvcStarting && s != health.SvcStopping
		}, nil); r != nil {
			return *r
		}
		if svc, _, err = h.P.ServiceState(name); err != nil {
			return internalErr(err)
		}
		if svc == health.SvcPaused {
			return pausedErr(name)
		}
	}
	if svc != health.SvcRunning {
		if ctx.Err() != nil { // never mutate for a cancelled request
			return ctxResp(name, "start")
		}
		switch svc {
		case health.SvcMissing:
			path, err := h.P.ConfigPath(name) // never construct paths ourselves
			if err != nil {
				return internalErr(err)
			}
			if err := h.P.InstallTunnel(path); err != nil {
				// Installed concurrently (e.g. by the WireGuard GUI)? Then
				// just wait for it like any other start.
				if s, _, serr := h.P.ServiceState(name); serr != nil || s == health.SvcMissing {
					return internalErr(err)
				}
			}
		case health.SvcStopped, health.SvcFailed:
			if err := h.P.StartService(name); err != nil {
				return internalErr(err)
			}
		}
		if r := h.waitService(ctx, name, "start", deadline, func(s health.ServiceState) bool { return s == health.SvcRunning }, startFailed(name)); r != nil {
			return *r
		}
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
	svc, _, err := h.P.ServiceState(name)
	if err != nil {
		return internalErr(err)
	}
	if svc == health.SvcMissing {
		return h.statusResp(name, false)
	}
	if ctx.Err() != nil { // never mutate for a cancelled request
		return ctxResp(name, "stop")
	}
	deadline := h.now().Add(h.timeout())
	if err := h.P.UninstallTunnel(name); err != nil {
		return internalErr(err)
	}
	if r := h.waitService(ctx, name, "stop", deadline, func(s health.ServiceState) bool { return s == health.SvcMissing }, nil); r != nil {
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
