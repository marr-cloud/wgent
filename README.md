# wgent

[![CI](https://github.com/marr-cloud/wgent/actions/workflows/ci.yml/badge.svg)](https://github.com/marr-cloud/wgent/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`wgent` lets AI agents (and you) start, stop and inspect WireGuard tunnels on Windows without a UAC prompt. A tiny Windows service running as SYSTEM does the privileged work; the unprivileged `wgent` CLI talks to it over a locked-down named pipe. Every command supports `--json`, and tunnel health is judged from real WireGuard handshake data, not just from whether a Windows service is running. An optional tray icon shows the state of your default tunnel at a glance.

**Why not just `wireguard.exe /installtunnelservice`?** It needs an elevated shell every single time, and it tells you nothing about whether the tunnel actually works. `wgent` needs admin once (to install its service) and then reports `up`, `down`, `degraded` and friends with a reason.

## Quickstart

```powershell
go install github.com/marr-cloud/wgent/cmd/wgent@latest
sudo wgent service install          # one time, elevated
wgent config set-default home       # name of a tunnel imported in WireGuard
wgent up
```

```text
home: up (handshake 4s ago, rx 1.2 KiB, tx 3.4 KiB)
```

## Requirements

- Windows 10 or 11 (amd64 or arm64).
- [WireGuard for Windows](https://www.wireguard.com/install/) installed in `C:\Program Files\WireGuard`.
- Your tunnels already imported in the official WireGuard app. `wgent` controls existing tunnels; it never creates or edits them.
- Go 1.25+ only if you install with `go install`.

## Install

Either install with Go:

```powershell
go install github.com/marr-cloud/wgent/cmd/wgent@latest
```

or download the zip for your architecture from [GitHub Releases](https://github.com/marr-cloud/wgent/releases), extract it, and put `wgent.exe` somewhere on your `PATH`.

## One-time setup

Installing the service needs elevation once:

```powershell
sudo wgent service install
```

Use Windows' built-in `sudo` (Settings > System > For developers > Enable sudo, inline mode) or run the command from an elevated terminal. The command copies `wgent.exe` to `%ProgramFiles%\wgent\wgent.exe`, registers the `wgent` service (automatic start, LocalSystem), restricts the pipe to your user, and starts it. After that, no command needs elevation.

If you later upgrade the binary, run `sudo wgent service install` again to refresh the installed copy.

## Configuration

The config file lives at `$XDG_CONFIG_HOME\wgent\config.toml` if `XDG_CONFIG_HOME` is set, otherwise `%USERPROFILE%\.config\wgent\config.toml`. It is optional.

```toml
default_tunnel = "home"

[tray]
poll_seconds = 2
```

| Key | Meaning |
| --- | --- |
| `default_tunnel` | Tunnel used when a command omits `[tunnel]`. |
| `tray.poll_seconds` | How often the tray polls the service (default 2). |

```text
$ wgent config set-default home
default tunnel set to home
$ wgent config
path: C:\Users\you\.config\wgent\config.toml
default_tunnel = "home"

[tray]
  poll_seconds = 2
```

Tunnel names must match `^[A-Za-z0-9_=+.-]{1,32}$`.

## Usage

```text
wgent list [--json]
wgent status [tunnel] [--json]
wgent up [tunnel] [--json]
wgent down [tunnel] [--json]
wgent toggle [tunnel] [--json]
wgent config
wgent config set-default <tunnel>
wgent tray [--autostart on|off] [--foreground]
wgent service install|uninstall
wgent version
wgent help
```

If `[tunnel]` is omitted, `default_tunnel` from the config file is used. `up` and `down` wait up to 15 seconds for the tunnel to reach the requested state. `toggle` brings the tunnel down unless it is currently `down`, in which case it brings it up.

Sample output (the examples below are illustrative; the exact numbers will differ):

```text
$ wgent list
* home: up (handshake 4s ago, rx 1.2 KiB, tx 3.4 KiB)
  office: down
  lab: degraded — no handshake with any peer yet

$ wgent status
home: up (handshake 4s ago, rx 1.2 KiB, tx 3.4 KiB)

$ wgent down
home: down

$ wgent down
home: down (no change)

$ wgent status nope
error: tunnel "nope" not found
hint: run "wgent list"
```

`list` marks the default tunnel with `*`. Errors go to stderr in human mode.

## For AI agents

Always call `wgent` with `--json`. Success prints a single JSON object on stdout. Failure prints an error object on stdout (also with `--json`) and a non-zero exit code. Decide on the exit code first, then parse the JSON.

### Status object

Returned by `status`, `up`, `down` and `toggle`:

```json
{
  "tunnel": "home",
  "state": "up",
  "reason": "",
  "changed": true,
  "service": "running",
  "peers": [
    {
      "endpoint": "203.0.113.7:51820",
      "last_handshake": "2026-11-23T10:15:02Z",
      "handshake_age_s": 4,
      "rx_bytes": 1234,
      "tx_bytes": 3456
    }
  ]
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `tunnel` | string | Tunnel name. |
| `state` | string | One of `up`, `down`, `degraded`, `error`, `pending` (see table below). |
| `reason` | string | Human-readable explanation; empty when nothing needs explaining. |
| `changed` | bool | `true` if this command actually started or stopped the tunnel; `false` if it was already in the requested state (idempotent) or the command was read-only. |
| `service` | string | Windows service state of the tunnel: `missing`, `stopped`, `starting`, `running`, `stopping`. |
| `peers` | array | One entry per peer (empty when the tunnel is not running). |
| `peers[].endpoint` | string | Peer endpoint as reported by WireGuard. |
| `peers[].last_handshake` | string or null | RFC 3339 time of the last handshake; `null` if the peer never handshook. |
| `peers[].handshake_age_s` | integer or null | Seconds since the last handshake; `null` if none. |
| `peers[].rx_bytes`, `tx_bytes` | integer | Bytes received / sent. |
| `default` | bool | Only present (and `true`) in `list` entries for the default tunnel. |

### List shape

```json
{
  "default_tunnel": "home",
  "tunnels": [
    { "tunnel": "home", "state": "up", "reason": "", "changed": false, "service": "running", "peers": [], "default": true },
    { "tunnel": "office", "state": "down", "reason": "", "changed": false, "service": "stopped", "peers": [] }
  ]
}
```

`default_tunnel` is `""` when none is configured; `tunnels` is `[]` when there are no tunnels.

### Error object

```json
{"error":{"code":"service_not_installed","message":"wgent service is not running","hint":"sudo wgent service install"}}
```

`hint` is omitted when empty. Codes: `usage`, `no_default_tunnel`, `invalid_tunnel_name`, `service_not_installed`, `protocol_mismatch`, `tunnel_not_found`, `timeout`, `internal`.

### States

| State | Meaning |
| --- | --- |
| `up` | Tunnel service running and the newest peer handshake is less than 180 s old. |
| `down` | Tunnel service not running (stopped or not present). `reason` is empty. |
| `degraded` | Service running but unhealthy: no peers, no handshake yet, or the last handshake is 180 s or older. |
| `pending` | The tunnel service is starting or stopping. Poll again shortly. |
| `error` | Service running but its stats could not be read; `reason` has the cause. |

Keepalive caveat: WireGuard only handshakes when there is traffic. A tunnel without `PersistentKeepalive` that has been idle for more than 180 s reports `degraded` ("peer may be unreachable (or idle without PersistentKeepalive)") even though it is fine. Generate some traffic, or set `PersistentKeepalive = 25` on the peer, before treating `degraded` as a failure.

### Exit codes

| Code | Meaning | JSON `error.code` |
| --- | --- | --- |
| 0 | Success. | |
| 1 | Generic error. | `internal` |
| 2 | Usage error, no default tunnel, or invalid tunnel name. | `usage`, `no_default_tunnel`, `invalid_tunnel_name` |
| 3 | wgent service not installed or unreachable, or protocol mismatch. | `service_not_installed`, `protocol_mismatch` |
| 4 | Tunnel not found. | `tunnel_not_found` |
| 5 | Timed out waiting for up/down (15 s). | `timeout` |

### Idempotency

`up` on a tunnel that is already up, and `down` on one that is already down, succeed with exit code 0 and `"changed": false`. Agents can call them unconditionally.

### Example workflow

Bash:

```bash
state=$(wgent status --json | jq -r '.state') || exit 1
if [ "$state" != "up" ]; then
  wgent up --json >/dev/null || { echo "up failed (exit $?)"; exit 1; }
fi
[ "$(wgent status --json | jq -r '.state')" = "up" ] || { echo "tunnel not up"; exit 1; }
```

PowerShell:

```powershell
$s = wgent status --json | ConvertFrom-Json
if ($s.state -ne 'up') {
    wgent up --json | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "wgent up failed (exit $LASTEXITCODE)" }
}
$s = wgent status --json | ConvertFrom-Json
if ($s.state -ne 'up') { throw "tunnel state: $($s.state) $($s.reason)" }
```

Agents should never run `wgent service install` themselves; if they get exit code 3, they should ask the human to run `sudo wgent service install`. A short version of this contract is in [AGENTS.md](AGENTS.md).

## Tray

```powershell
wgent tray                      # start the tray icon in the background
wgent tray --autostart on       # start it at logon (HKCU Run entry); "off" removes it
wgent tray --foreground         # run attached to this console
```

The tray dot reflects the default tunnel: green is `up`, grey is `down` or unknown, amber is `pending` (or a toggle in progress), red is `degraded`/`error` or a failure. Left click toggles the default tunnel; right click opens a menu with every tunnel and Quit. The tray polls every `tray.poll_seconds`.

## Security model

- The service runs as LocalSystem, but accepts only four operations: `list`, `status`, `up`, `down`, each with a validated tunnel name that must exist in the WireGuard configurations directory. It never accepts file paths or arbitrary commands, so it cannot be used to run or import arbitrary configs.
- The named pipe `\\.\pipe\wgent` is accessible only to SYSTEM, Administrators and the SID of the user who ran `wgent service install`. Other local users cannot talk to it.
- The service binary is copied to `%ProgramFiles%\wgent\wgent.exe`, a location non-admin users cannot write to, so the SYSTEM service cannot be hijacked by replacing a user-writable binary.
- Anyone who can already run code as the installing user can start and stop your tunnels. That is the point; it does not grant access to tunnel keys or configs.

## Uninstall

```powershell
sudo wgent service uninstall
```

This stops and removes the service and deletes `%ProgramFiles%\wgent\wgent.exe`. If the file is still in use it is scheduled for deletion at the next reboot and the command says so. Run `wgent tray --autostart off` first if you enabled tray autostart.

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| `service_not_installed` (exit 3), "wgent service is not running" | The service is not installed or not running. Run `sudo wgent service install`. |
| `protocol_mismatch` (exit 3) | The CLI and the installed service are different versions, usually after an upgrade. Run `sudo wgent service install` to refresh the service. |
| `no_default_tunnel` (exit 2) | Pass a tunnel name or run `wgent config set-default <tunnel>`. |
| `tunnel_not_found` (exit 4) | The tunnel is not imported in the WireGuard app. Check `wgent list`. |
| `degraded` | Usually one of: peer unreachable or wrong endpoint/keys; no traffic yet (no handshake until the first packet); idle for 180 s or more without `PersistentKeepalive`; tunnel has no peers. Read `reason`, then send some traffic. |
| `timeout` (exit 5) | The tunnel service did not reach the requested state in 15 s. Check the WireGuard app's log. |

## Development

```powershell
go test ./...
go vet ./...
```

The CLI, protocol and health logic are pure Go and build on any OS (CI also runs `GOOS=linux go vet ./...`); Windows-specific code lives in `*_windows.go` files. Releases are built by GoReleaser when a `v*` tag is pushed.

## License

[MIT](LICENSE)
