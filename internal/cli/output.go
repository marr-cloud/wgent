package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/marr-cloud/wgent/internal/api"
	"github.com/marr-cloud/wgent/internal/health"
)

// formatSize renders a byte count as B / KiB / MiB / GiB (one decimal above B).
func formatSize(n uint64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%.1f KiB", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1f MiB", float64(n)/(k*k))
	default:
		return fmt.Sprintf("%.1f GiB", float64(n)/(k*k*k))
	}
}

// statusLine renders the one-line human form of a status. showChanged appends
// " (no change)" for up/down results that did nothing.
func statusLine(s api.Status, showChanged bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", s.Tunnel, s.State)
	for _, p := range s.Peers {
		if p.HandshakeAgeS != nil {
			fmt.Fprintf(&b, " (handshake %ds ago, rx %s, tx %s)", *p.HandshakeAgeS, formatSize(p.RxBytes), formatSize(p.TxBytes))
			break
		}
	}
	if s.Reason != "" {
		fmt.Fprintf(&b, " — %s", s.Reason)
	}
	if showChanged && !s.Changed && (s.State == health.StateUp || s.State == health.StateDown) {
		b.WriteString(" (no change)")
	}
	return b.String()
}

func writeJSON(w io.Writer, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		buf.Reset()
		buf.WriteString(`{"error":{"code":"internal","message":"encode output"}}` + "\n")
	}
	_, _ = w.Write(buf.Bytes())
}
