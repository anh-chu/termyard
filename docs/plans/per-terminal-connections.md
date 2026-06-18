# Plan: per-terminal connections for remote sessions

Companion to `docs/adr-per-terminal-connections.md`. This is the build order.

Goal: a remote terminal uses its own dedicated peer connection, one to one with the browser WebSocket, reusing the local PTY bridge. The shared peer link is reduced to control plane only. Blank on switch and firehose starvation are gone by construction.

Non goals: hub topology, the both-firewalled relay case, replacing the control link's state sync. Those are separate.

## Current code map

- Route split: `pkg/server/server.go:1693` and `:1704`. `/ws/session` with a `host` param goes to `handleRemoteSession` (remote, multiplexed); without it goes to `ptyHandler.HandleSession` (local, one to one, reliable).
- Local bridge to reuse: `pkg/ws/pty_terminal.go` `HandleSession`. Spawns `tmux.NewPTYSession`, two goroutines pump PTY to WS (binary) and WS to PTY, answers pings locally.
- Peer control link: dialed at `pkg/peer/supervisor.go:322` as `ws://addr/ws/peer`, accepted at `pkg/server/server.go:1716` (`opts.PeerHandler.HandlePeer`). Roles set by `InitiatedByUs` in `supervisor.go`.
- Multiplexed remote path to remove later: `pkg/peer/pty_relay.go`, `pkg/peer/pty_manager.go`, the PTY frame protocol and binary demux in `pkg/peer/session.go`, the ack and force-redial code in `handleRemoteSession`.
- Manager and connection registry: `pkg/peer/manager.go` (`PeerConnection`, `GetPeerConnection`, host registry).

## Design in one paragraph

The browser opens `/ws/session?host=H&name=S` as today. The viewer server allocates a stream id and a one time token, sends an `open-terminal` control message to H over the existing peer link, and waits. The control-link dialer (whichever side that is) opens a fresh data connection to the other side at the same address the peer link uses, path `/ws/peer-stream`, presenting the token. The two ends look up the pending stream by token: the host end spawns the tmux PTY and runs the existing bridge pump against this connection, the viewer end splices this connection to the browser WebSocket byte for byte. One terminal, one connection, no shared writer.

## Phases

Each phase builds, vets, tests, and is independently shippable behind a flag where noted.

### Phase 0: keep instrumentation (done)

Backend trace (`pkg/peer/trace.go`), pprof, and the per-message frontend logging stay in. They are the acceptance harness for Phase 6. Do not remove yet.

### Phase 1: control message and data-connection handshake

Outcome: a typed `open-terminal` control message and a `/ws/peer-stream` endpoint exist, authenticated like `/ws/peer`, that can be correlated to a pending request by token. Nothing wired to terminals yet.

- Add `MsgOpenTerminal` to `pkg/peer/protocol.go` carrying `{StreamID, Session, Cols, Rows, Token, ViewerHostID}`.
- Add a pending-streams registry keyed by token (new small type, likely in `pkg/peer/` next to the manager). Entry holds the request fields and a channel the accepting side signals when the data connection arrives. TTL it (a few seconds) so a missed dial cleans up.
- Add the `/ws/peer-stream` route next to `/ws/peer` in `pkg/server/server.go`, using the same auth as the peer link. On accept it reads a small handshake frame with the token, looks up the pending entry, and hands the raw connection to the resolver. If no entry, close.
- Add a dial helper that opens an extra connection to a peer at the control-link address with path `/ws/peer-stream` and sends the token handshake. Mirror `supervisor.go:322`'s URL build and dialer. It must reuse the same address and TLS settings as the live control link for that peer.

Verify: a unit or manual test that registers a token, dials `/ws/peer-stream` with it, and confirms both ends resolve to the same stream id. No terminal logic yet.

### Phase 2: host end bridge over the data connection

Outcome: given a resolved data connection and a session name, the host spawns the tmux PTY and pumps it to that connection, reusing the local bridge logic.

- Factor the pump loop out of `pkg/ws/pty_terminal.go` `HandleSession` into a function that takes an already-open `*websocket.Conn`, a session name, and cols/rows, then runs the existing two goroutines (PTY to conn binary, conn to PTY, local ping answer). Keep `HandleSession` as a thin caller so local behavior is unchanged.
- The host end of `/ws/peer-stream` calls this function with the session from the pending entry.

Verify: from a quick harness, open a data connection to a host for a real local session and confirm bytes flow and input works. Local `/ws/session` still behaves identically (regression check).

### Phase 3: viewer end splice and route switch

Outcome: `handleRemoteSession` no longer multiplexes. It allocates the token, sends `open-terminal`, waits for the data connection, then splices browser WebSocket and data connection.

- Rewrite `handleRemoteSession` (`pkg/server/server.go:313`):
  1. Upgrade the browser WebSocket.
  2. Allocate stream id and token, register the pending entry.
  3. Send `MsgOpenTerminal` over the control link to `hostID`.
  4. If the local side is the control-link dialer, dial `/ws/peer-stream` now. Otherwise wait for the host to dial back (the pending entry's channel resolves either way).
  5. On resolve, splice: copy browser to data conn and data conn to browser, both directions, until either closes. Translate the browser's text ping locally as today.
  6. On timeout (no data connection within the TTL), return an error so the browser reconnects.
- The control-link receiver handles `MsgOpenTerminal`: if this side is the dialer, dial `/ws/peer-stream` back to the requester with the token; the host role is whoever owns the session.

Put the new path behind a flag or a capability check so a peer that does not speak `peer-stream` still falls back to the old multiplexed path during rollout. Negotiate the capability in the existing peer handshake or gate on a version field.

Verify with the trace harness: a remote open shows `open-terminal` then a `peer-stream` accept then first bytes, with no shared-lane events. Switch between two remote sessions repeatedly with a third session firehosing; the switched terminal paints immediately (the Phase 6 acceptance).

### Phase 4: remove the multiplexed PTY path

Outcome: dead code gone once Phase 3 is the only path.

- Delete `pkg/peer/pty_relay.go`.
- Delete the multiplexed output path in `pkg/peer/pty_manager.go` and its `Open`. Keep only what other features still need, if anything.
- Remove the PTY frame protocol and binary demux from `pkg/peer/session.go` (`EncodePTYFrame`, `FramePTYOutput`, the binary frame branch).
- Remove the ack and force-redial machinery: `MsgPTYOpenAck`, `WaitForOpenAck`, the ack-timeout goroutine.
- Remove the capability fallback added in Phase 3.

Verify: build, vet, full `go test ./...`, and a grep that none of the deleted symbols are referenced. Trace shows only control plus per-stream events.

### Phase 5: lifecycle and resilience

Outcome: per-connection failures are local and recover via the existing browser reconnect.

- Data connection keepalive: the browser already pings every 10s and the server answers locally in the bridge. Confirm both directions of the splice forward closes promptly so the browser `onclose` fires and reconnects (`useTerminal.ts` already reconnects after 2s).
- Optional `TCP_USER_TIMEOUT` on Linux data connections as a defense for half open links, best effort, skip if it fights the dialer.
- Confirm a dead data connection does not affect the control link or other terminals (it is a separate socket, so this should hold by construction; verify with a kill test).

Verify: kill one terminal's data connection mid-stream; only that terminal reconnects, others and the control link are untouched.

### Phase 6: acceptance and cleanup

Outcome: the original bug is gone, measured.

- Repro the exact failing scenario: two remote sessions, one firehosing, switch into the other. It must paint within a second. Compare against the captured baseline (the 15 to 18s blank).
- Optional netem on the link (added latency and loss) to confirm a slow link no longer blanks a freshly opened terminal.
- Once confident, strip or gate the trace and pprof instrumentation behind a debug flag.

## Risks

- Connection count grows with open terminals. Acceptable, local mode already does this. Watch fd limits on the host for many concurrent viewers.
- Token handshake is a new trust boundary on `/ws/peer-stream`. It must reuse the peer link's auth, not just the token; the token only correlates, it does not authorize.
- Rollout skew between peer versions. The Phase 3 capability gate covers this; do not delete the fallback until both ends are upgraded (Phase 4).
- Role and direction logic (who dials, who spawns the PTY) is the subtle part. It is independent of dial direction by design; cover it with an explicit test for both combinations (viewer is dialer, host is dialer).

## Rollback

Phases 1 and 2 add code without changing behavior. Phase 3 is flagged. If the per-stream path misbehaves, disable the flag to fall back to the multiplexed path until Phase 4. After Phase 4 the fallback is gone, so do not start Phase 4 until Phase 3 has soaked.

## Suggested delegation

This touches the peer protocol, async lifecycle, and shared transport, so use the full `/feature` chain (Explore, Plan, worker, reviewer, worker) per phase, not `/feature-light`. Phase 3 in particular changes a contract and timing and must go through review. Run `oracle` on the Phase 1 and Phase 3 designs before implementing them.
