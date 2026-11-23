# wgent: contract for AI agents

`wgent` starts, stops and inspects WireGuard tunnels on Windows without UAC.
Full reference: the "For AI agents" section of [README.md](README.md).

## Rules

- Always pass `--json`. Success prints one JSON object on stdout; failure prints
  `{"error":{"code","message","hint"}}` on stdout. Branch on the exit code first.
- Commands: `wgent list|status|up|down|toggle [tunnel] --json`. Omit `[tunnel]`
  to use `default_tunnel` from the config file.
- `up`/`down` are idempotent and wait up to 15 s. Check `changed` (false = already
  in that state) and then `state`. Exit 0 does not mean `up`: `up` can return
  `degraded` or `pending`. Always check `state`.
- Only `list|status|up|down|toggle` honour `--json`.
- Never run `wgent service install/uninstall` or `wgent tray` yourself; those are
  for the human (one-time elevated setup / desktop UI).
- Tunnel names must match `^[A-Za-z0-9_=+.-]{1,32}$`. Paths are never accepted.

## States (`state`)

`up` (handshake < 180 s), `down`, `degraded` (running but no recent handshake;
may be idle without PersistentKeepalive), `pending` (starting/stopping, re-poll),
`error` (tunnel service failed, paused, or stats unreadable; read `reason`).

## Exit codes

0 ok, 1 generic error (incl. tunnel service failed while starting), 2 usage /
no default tunnel / invalid name, 3 service not installed, not responding or not
genuine, or protocol mismatch, 4 tunnel not found, 5 timeout.

On exit 3 tell the human to run `sudo wgent service install` (the hint says so).
