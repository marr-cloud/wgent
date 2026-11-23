package tray

import "strings"

// AutostartValueName is the HKCU Run value name used for autostart.
const AutostartValueName = "wgent"

// RunKeyPath is the HKCU key holding per-user autostart entries.
const RunKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// AutostartCommand builds the Run value: conhost --headless keeps the console
// subsystem exe from flashing a window at logon.
func AutostartCommand(systemRoot, exe string) string {
	systemRoot = strings.TrimRight(systemRoot, `\/`)
	return `"` + systemRoot + `\System32\conhost.exe" --headless "` + exe + `" tray --foreground`
}
