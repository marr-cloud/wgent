package tray

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/marr-cloud/wgent/internal/api"
	"github.com/marr-cloud/wgent/internal/health"
)

func TestDotICOHeader(t *testing.T) {
	c := color.RGBA{0x2E, 0xA0, 0x43, 0xFF}
	b := DotICO(c)
	if !bytes.Equal(b[0:6], []byte{0, 0, 1, 0, 1, 0}) {
		t.Fatalf("bad ICONDIR: % x", b[0:6])
	}
	if b[6] != 32 || b[7] != 32 {
		t.Fatalf("entry size = %dx%d, want 32x32", b[6], b[7])
	}
	if off := binary.LittleEndian.Uint32(b[18:22]); off != 22 {
		t.Fatalf("image offset = %d, want 22", off)
	}
	if size := binary.LittleEndian.Uint32(b[14:18]); int(size) != len(b)-22 {
		t.Fatalf("image size = %d, want %d", size, len(b)-22)
	}
	if !bytes.HasPrefix(b[22:], []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("missing PNG signature")
	}
	img, err := png.Decode(bytes.NewReader(b[22:]))
	if err != nil {
		t.Fatal(err)
	}
	if r := img.Bounds(); r.Dx() != 32 || r.Dy() != 32 {
		t.Fatalf("bounds = %v", r)
	}
	r, g, bl, a := img.At(16, 16).RGBA()
	if uint8(r>>8) != c.R || uint8(g>>8) != c.G || uint8(bl>>8) != c.B || uint8(a>>8) != c.A {
		t.Fatalf("center = %d %d %d %d, want %v", r>>8, g>>8, bl>>8, a>>8, c)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
		t.Fatalf("corner alpha = %d, want 0", a)
	}
}

func TestStateColor(t *testing.T) {
	cases := map[health.State]color.RGBA{
		health.StateUp:       ColorUp,
		health.StateDown:     ColorDown,
		health.StatePending:  ColorPending,
		health.StateDegraded: ColorError,
		health.StateError:    ColorError,
		"weird":              ColorDown,
	}
	for s, want := range cases {
		if got := StateColor(s); got != want {
			t.Errorf("StateColor(%q) = %v, want %v", s, got, want)
		}
	}
	if ColorUp != (color.RGBA{0x2E, 0xA0, 0x43, 0xFF}) || ColorError != (color.RGBA{0xCF, 0x22, 0x2E, 0xFF}) {
		t.Error("palette mismatch")
	}
}

func TestTooltip(t *testing.T) {
	if got := Tooltip("work", health.StateUp, ""); got != "wgent: work up" {
		t.Errorf("got %q", got)
	}
	if got := Tooltip("work", health.StateDegraded, "no handshake"); got != "wgent: work degraded — no handshake" {
		t.Errorf("got %q", got)
	}
	long := Tooltip("work", health.StateError, strings.Repeat("é", 300))
	if n := len([]rune(long)); n > MaxTooltip {
		t.Errorf("tooltip has %d runes, want <= %d", n, MaxTooltip)
	}
	if got := Truncate("short", 127); got != "short" {
		t.Errorf("got %q", got)
	}
}

func TestViewFor(t *testing.T) {
	v := ViewFor("", nil, nil)
	if v.Tooltip != "wgent: no default tunnel" || v.Color != ColorDown {
		t.Errorf("no default: %+v", v)
	}
	v = ViewFor("work", nil, &api.Error{Code: api.CodeServiceNotInstalled, Message: "x"})
	if v.Tooltip != "wgent: service not running" || v.Color != ColorDown {
		t.Errorf("no service: %+v", v)
	}
	v = ViewFor("work", nil, &api.Error{Code: api.CodeTunnelNotFound, Message: "tunnel not found"})
	if v.Color != ColorError || !strings.HasPrefix(v.Tooltip, "wgent: ") {
		t.Errorf("other error: %+v", v)
	}
}

func TestViewForStatus(t *testing.T) {
	v := ViewFor("work", &api.Status{Tunnel: "work", State: health.StateUp}, nil)
	if v.Tooltip != "wgent: work up" || v.Color != ColorUp {
		t.Errorf("up: %+v", v)
	}
}

func TestSameNames(t *testing.T) {
	if !SameNames([]string{"a", "b"}, []string{"a", "b"}) || SameNames([]string{"a"}, []string{"a", "b"}) || SameNames([]string{"a", "b"}, []string{"a", "c"}) {
		t.Error("SameNames wrong")
	}
}

func TestAutostartCommand(t *testing.T) {
	got := AutostartCommand(`C:\Windows\`, `C:\Program Files\wgent\wgent.exe`)
	want := `"C:\Windows\System32\conhost.exe" --headless "C:\Program Files\wgent\wgent.exe" tray --foreground`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}
