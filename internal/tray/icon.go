// Package tray implements the wgent system tray icon. This file holds the
// pure (OS-independent) helpers; the systray wiring is in tray_windows.go.
package tray

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"

	"github.com/marr-cloud/wgent/internal/api"
	"github.com/marr-cloud/wgent/internal/health"
)

// Palette for the tray dot.
var (
	ColorUp      = color.RGBA{0x2E, 0xA0, 0x43, 0xFF}
	ColorDown    = color.RGBA{0x8C, 0x8C, 0x8C, 0xFF}
	ColorPending = color.RGBA{0xD2, 0x99, 0x22, 0xFF}
	ColorError   = color.RGBA{0xCF, 0x22, 0x2E, 0xFF}
)

// MaxTooltip is the Windows tray tooltip limit (in characters).
const MaxTooltip = 127

// StateColor maps a tunnel state to its dot color. Unknown states are grey.
func StateColor(s health.State) color.RGBA {
	switch s {
	case health.StateUp:
		return ColorUp
	case health.StatePending:
		return ColorPending
	case health.StateDegraded, health.StateError:
		return ColorError
	}
	return ColorDown
}

// Truncate shortens s to at most n runes, ending with an ellipsis if cut.
func Truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Tooltip formats "wgent: <tunnel> <state>[ — <reason>]", at most MaxTooltip runes.
func Tooltip(tunnel string, state health.State, reason string) string {
	s := "wgent: " + tunnel + " " + string(state)
	if reason != "" {
		s += " — " + reason
	}
	return Truncate(s, MaxTooltip)
}

// View is what the tray shows: a dot color and a tooltip.
type View struct {
	Color   color.RGBA
	Tooltip string
}

// ViewFor computes the view for the default tunnel from a status result or error.
func ViewFor(tunnel string, st *api.Status, err error) View {
	if tunnel == "" {
		return View{ColorDown, "wgent: no default tunnel"}
	}
	if err != nil {
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == api.CodeServiceNotInstalled {
			return View{ColorDown, "wgent: service not running"}
		}
		return View{ColorError, Truncate("wgent: "+err.Error(), MaxTooltip)}
	}
	if st == nil {
		return View{ColorDown, Truncate("wgent: "+tunnel+" unknown", MaxTooltip)}
	}
	return View{StateColor(st.State), Tooltip(tunnel, st.State, st.Reason)}
}

// SameNames reports whether two name lists are identical (order-sensitive).
func SameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// DotICO renders a 32x32 anti-aliased filled circle as a PNG inside an ICO container.
func DotICO(c color.RGBA) []byte {
	const size = 32
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4 // supersampling per axis
	cx, cy, rad := float64(size)/2, float64(size)/2, float64(size)/2-1.5
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			in := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x) + (float64(sx)+0.5)/ss
					py := float64(y) + (float64(sy)+0.5)/ss
					if math.Hypot(px-cx, py-cy) <= rad {
						in++
					}
				}
			}
			if in > 0 {
				img.SetNRGBA(x, y, color.NRGBA{c.R, c.G, c.B, uint8(in * int(c.A) / (ss * ss))})
			}
		}
	}
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)

	var out bytes.Buffer
	out.Write([]byte{0, 0, 1, 0, 1, 0})                           // ICONDIR: reserved, type=icon, count=1
	out.Write([]byte{size, size, 0, 0})                           // width, height, colors, reserved
	_ = binary.Write(&out, binary.LittleEndian, [2]uint16{1, 32}) // planes, bpp
	_ = binary.Write(&out, binary.LittleEndian, uint32(pngBuf.Len()))
	_ = binary.Write(&out, binary.LittleEndian, uint32(22))
	out.Write(pngBuf.Bytes())
	return out.Bytes()
}
