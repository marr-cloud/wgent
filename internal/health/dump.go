// Package health parses WireGuard dump output and computes a tunnel verdict.
package health

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Peer is one peer line of `wg show <iface> dump`.
type Peer struct {
	PublicKey     string
	Endpoint      string    // empty when unknown
	LastHandshake time.Time // zero if never
	RxBytes       uint64
	TxBytes       uint64
}

// ParseDump parses the output of `wg show <iface> dump`. The first line
// (interface) is skipped; every further non-empty line must have 8 tab fields.
func ParseDump(out string) ([]Peer, error) {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	var peers []Peer
	first := true
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if first {
			first = false
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 8 {
			return nil, fmt.Errorf("dump line %d: expected 8 fields, got %d", i+1, len(f))
		}
		hs, err := strconv.ParseInt(f[4], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("dump line %d: invalid latest-handshake %q", i+1, f[4])
		}
		rx, err := strconv.ParseUint(f[5], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("dump line %d: invalid transfer-rx %q", i+1, f[5])
		}
		tx, err := strconv.ParseUint(f[6], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("dump line %d: invalid transfer-tx %q", i+1, f[6])
		}
		p := Peer{PublicKey: f[0], Endpoint: f[2], RxBytes: rx, TxBytes: tx}
		if p.Endpoint == "(none)" {
			p.Endpoint = ""
		}
		if hs > 0 {
			p.LastHandshake = time.Unix(hs, 0)
		}
		peers = append(peers, p)
	}
	return peers, nil
}
