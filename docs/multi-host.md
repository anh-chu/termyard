# Multi-host (symmetric peer-to-peer)

guppi runs as a symmetric peer-to-peer mesh. Every machine runs the same
`guppi server` process — there is no hub or peer role anymore. Any node can
connect to any other node from its dashboard.

## How it works

1. On each machine, run `guppi server` (no flags needed beyond `--port` if
   you want to change the default 7654).
2. Open each dashboard in your browser and set a password (one-time).
3. On node A's dashboard, open **Settings → Machines → Connect to another
   machine**. Enter node B's `host:port` and node B's dashboard password.
4. A and B now share sessions bidirectionally. The link auto-recovers if it
   drops.

You only need to run the Connect flow once per pair, from whichever side can
reach the other. Bootstrap is idempotent — re-running just refreshes the
address.

## Reachability

guppi serves plain HTTP. The peer-to-peer link is plain WebSocket (`ws://`).
There is no built-in TLS, no certificate generation, and no pairing codes.

Use one of these for encryption / cross-network reachability:

- **Tailscale / WireGuard** (recommended) — gives each node a stable
  hostname and end-to-end encryption.
- **Reverse proxy** (Caddy, nginx) in front of `guppi server` if you want
  HTTPS for the browser side.

If neither machine can reach the other directly, no overlay network will
fix that for you. Pick the side that can reach the other and run Connect
from there — that side becomes the dialer; the other side just listens.

## Behaviour

- **Bidirectional**: once A↔B is up, A sees B's sessions and B sees A's.
- **Auto-reconnect**: enabled by default. Backoff 1s → 30s with jitter.
- **No transitivity**: if A↔B and A↔C are paired, B does not see C through
  A. You must pair B and C directly if you want that.
- **Disable / forget**: in the Machines panel, toggle Auto-reconnect off to
  stop dialing without removing the peer; click Forget to remove the peer
  entirely. Forget propagates over the live link so both sides clean up.
- **Local sessions stay functional** when a peer goes offline.

## Server flags

`guppi server` only takes:

- `--port` / `GUPPI_PORT` (default 7654)
- `--socket` / `GUPPI_SOCKET` (local notify CLI socket path)
- `--discovery-interval` / `GUPPI_DISCOVERY_INTERVAL` (default 2s)
- `--no-control-mode` / `GUPPI_NO_CONTROL_MODE`
- `--no-auth` / `GUPPI_NO_AUTH`

All peer configuration lives in `~/.config/guppi/peers.json` and is managed
through the UI. There is no `--hub`, no `--tls-*`, no `guppi pair`, no
`guppi peers` CLI.

## Migration from older guppi

Old `peers.json` entries are loaded automatically (legacy TLS fields are
ignored). You will need to re-pair only if the old setup relied on pinned
self-signed TLS certs to reach a non-system-trusted host — TLS is gone, so
the address either works on plain HTTP/WS or doesn't.
