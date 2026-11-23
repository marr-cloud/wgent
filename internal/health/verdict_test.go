package health

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var now = time.Unix(1795000000, 0)

func peerAgo(d time.Duration) Peer { return Peer{PublicKey: "k", LastHandshake: now.Add(-d)} }

func TestVerdictDown(t *testing.T) {
	for _, s := range []ServiceState{SvcMissing, SvcStopped} {
		if st, r := Verdict(s, nil, nil, now); st != StateDown || r != "" {
			t.Errorf("%s: %s %q", s, st, r)
		}
	}
}

func TestVerdictPending(t *testing.T) {
	if st, r := Verdict(SvcStarting, nil, nil, now); st != StatePending || r != "tunnel service is starting" {
		t.Errorf("%s %q", st, r)
	}
	if st, r := Verdict(SvcStopping, nil, nil, now); st != StatePending || r != "tunnel service is stopping" {
		t.Errorf("%s %q", st, r)
	}
}

func TestVerdictUp(t *testing.T) {
	if st, r := Verdict(SvcRunning, []Peer{peerAgo(30 * time.Second)}, nil, now); st != StateUp || r != "" {
		t.Errorf("%s %q", st, r)
	}
}

func TestVerdictStale(t *testing.T) {
	st, r := Verdict(SvcRunning, []Peer{peerAgo(200 * time.Second)}, nil, now)
	if st != StateDegraded || !strings.Contains(r, "200s") {
		t.Errorf("%s %q", st, r)
	}
	want := "last handshake 200s ago; peer may be unreachable (or idle without PersistentKeepalive)"
	if r != want {
		t.Errorf("reason %q", r)
	}
}

func TestVerdictThresholdBoundary(t *testing.T) {
	if st, _ := Verdict(SvcRunning, []Peer{peerAgo(HandshakeThreshold)}, nil, now); st != StateDegraded {
		t.Errorf("age == threshold: %s", st)
	}
	if st, _ := Verdict(SvcRunning, []Peer{peerAgo(HandshakeThreshold - time.Second)}, nil, now); st != StateUp {
		t.Errorf("age < threshold: %s", st)
	}
}

func TestVerdictNeverHandshook(t *testing.T) {
	st, r := Verdict(SvcRunning, []Peer{{PublicKey: "k"}}, nil, now)
	if st != StateDegraded || r != "no handshake with any peer yet" {
		t.Errorf("%s %q", st, r)
	}
}

func TestVerdictNoPeers(t *testing.T) {
	st, r := Verdict(SvcRunning, nil, nil, now)
	if st != StateDegraded || r != "tunnel has no peers" {
		t.Errorf("%s %q", st, r)
	}
}

func TestVerdictStatsError(t *testing.T) {
	st, r := Verdict(SvcRunning, nil, errors.New("boom"), now)
	if st != StateError || r != "cannot read tunnel stats: boom" {
		t.Errorf("%s %q", st, r)
	}
}

func TestVerdictUsesNewestPeer(t *testing.T) {
	if st, _ := Verdict(SvcRunning, []Peer{peerAgo(500 * time.Second), peerAgo(10 * time.Second)}, nil, now); st != StateUp {
		t.Errorf("%s", st)
	}
}
