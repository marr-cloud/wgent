package health

import (
	"fmt"
	"time"
)

type State string

const (
	StateUp       State = "up"
	StateDown     State = "down"
	StateDegraded State = "degraded"
	StateError    State = "error"
	StatePending  State = "pending" // transition in progress
)

type ServiceState string

const (
	SvcMissing  ServiceState = "missing"
	SvcStopped  ServiceState = "stopped"
	SvcStarting ServiceState = "starting"
	SvcRunning  ServiceState = "running"
	SvcStopping ServiceState = "stopping"
)

// HandshakeThreshold is the maximum handshake age considered healthy.
const HandshakeThreshold = 180 * time.Second

// Verdict computes the tunnel state and a human-readable reason (empty when
// nothing needs explaining). now is injected for determinism.
func Verdict(svc ServiceState, peers []Peer, statsErr error, now time.Time) (State, string) {
	switch svc {
	case SvcStarting:
		return StatePending, "tunnel service is starting"
	case SvcStopping:
		return StatePending, "tunnel service is stopping"
	case SvcRunning:
	default: // missing, stopped, unknown
		return StateDown, ""
	}
	if statsErr != nil {
		return StateError, "cannot read tunnel stats: " + statsErr.Error()
	}
	if len(peers) == 0 {
		return StateDegraded, "tunnel has no peers"
	}
	var newest time.Time
	for _, p := range peers {
		if p.LastHandshake.After(newest) {
			newest = p.LastHandshake
		}
	}
	if newest.IsZero() {
		return StateDegraded, "no handshake with any peer yet"
	}
	age := now.Sub(newest)
	if age < HandshakeThreshold {
		return StateUp, ""
	}
	return StateDegraded, fmt.Sprintf("last handshake %ds ago; peer may be unreachable (or idle without PersistentKeepalive)", int64(age/time.Second))
}
