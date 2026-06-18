# ADR: per-terminal connections for remote sessions

Status: proposed
Date: 2026-06-18
Owners: Anh

## Context

Remote terminals go blank when you switch into them, while local terminals never do. The two paths share the same frontend (`web/src/hooks/useTerminal.ts`) and the same backend PTY bridge concept (`tmux attach` in a PTY). The only difference is transport:

- Local: one browser WebSocket maps to one PTY, one to one, no sharing. `pkg/ws/pty_terminal.go` (`HandleSession`).
- Remote: every terminal plus all control traffic is multiplexed over a single peer connection with a single writer goroutine and a single socket. `pkg/peer/` (`PeerConnection`, `PTYRelay`, `PTYManager`, the hi/lo lanes, the frame protocol).

The remote path is where the blank happens.

## Evidence

We instrumented both sides (backend `pkg/peer/trace.go` plus per-message frontend logging) and captured a live repro. Then we proved the mechanism in isolation.

1. Trace of a blank switch (session `9`, viewed from devvm over the peer link to maclap):
   - `pty-open-sent` logged on the viewer.
   - The listener logged nothing for that stream id. The open frame never arrived.
   - About 4s later the ack timeout fired and tore the connection down, then a burst of `open-502` while the link redialed. Total blank around 15 to 18s.
   - In the same window a second session was firehosing output (4.6 MB, `pty-reader-exit b=4601077 reads=1428`) and the shared hi lane logged `enqueue-fail hi-lane full / pc closed`.

2. Head of line test (isolated, deterministic, `localhost`, shared rate-limited reader as the slow link):

   ```
   firehose 7 MB, hi-queue depth 1024, link 2 MB/s, control sent at +200ms
   MODEL A (one conn, one writer):   control-frame latency  2781.1 ms
   MODEL B (one conn per stream):    control-frame latency     0.1 ms
   ```

   The new terminal's open waits behind megabytes of another terminal's output on the shared writer. Give each terminal its own connection and the open is delivered immediately while the firehose drains in parallel.

## Root cause

All remote streams and the control plane share one `PeerConnection`: one writer goroutine, one socket, priority enforced only inside Go channels (the hi and lo lanes in `pkg/peer/manager.go`, drained in `pkg/peer/session.go:165`).

- PTY output is enqueued on the hi lane (`pkg/peer/pty_manager.go:117`, `EnqueueBinaryHi`).
- The new terminal's `pty-open` is also on the hi lane (`pkg/server/server.go`).
- A firehose on one stream fills the hi channel and saturates the socket, so the tiny open frame is stuck behind it.

Channel level priority cannot fix this. When the socket itself is the bottleneck (slow or lossy link, or a firehose), every frame waits on the same `writeFrame` regardless of lane. This is head of line blocking on a multiplexed connection. The earlier half open TCP stall we saw (stuck Send-Q, one dead stream freezing all others) is the same root: one connection shared by all streams.

## Prior attempts and why they did not hold

Each prior fix patched a symptom of the shared connection and moved the collision elsewhere.

- `tmux attach -d`, then reverted. Repaint workaround, not a transport fix.
- Move `pty-open` from the lo lane to the hi lane plus a per cycle hi burst cap (commit history). It escaped lo lane starvation but landed the open behind bulk output on the hi lane. This is the collision we measured now.
- App level ack with retry, then force-redial on missed ack. The detector works, but `pc.Close()` tears down the whole shared connection, which also kills unrelated streams and opens a multi second redial gap of 502s. It made recovery worse.

The pattern is lane shuffling. The next reassignment would just create the next collision. The problem is the shared connection, not the lane order.

## Decision

Give each remote terminal its own dedicated connection, one to one with the browser WebSocket, the same shape that makes local terminals reliable. Keep a single persistent control connection per peer for state, presence, session list, layout sync, and a new open-terminal signal. The control connection carries no bulk PTY output, so it cannot be starved.

Shape:

- Control connection: the existing peer link (`/ws/peer`). Tiny messages only.
- Data connection: one per terminal. Carries exactly one PTY stream. No framing, no multiplexing, no lanes. A byte pump on the viewer end, the existing local PTY bridge on the host end (`pkg/ws/pty_terminal.go` logic).
- A firehose on one terminal fills only its own socket. A dead terminal closes only its own connection. Both failure modes disappear by construction.

Direction and NAT. The data connection is dialed in the same direction as the control link (the control-link dialer opens data connections too). Whoever holds the browser sends open-terminal over the control link; the dialer side performs the actual dial; a one time token correlates the new connection to the browser request and to the PTY spawn. Roles (host spawns the PTY, viewer bridges to the browser) are negotiated in the data-connection handshake and are independent of who dialed.

This is a reverse tunnel when the host is the dialer, and a direct dial when the viewer is the dialer. Either way no inbound is required to a side that did not already accept the control link.

## Connectivity validation

We tested outbound dialing from the constrained mac.

- Go dialing a public host on 443 from maclap works (`1.1.1.1`, `github.com`, `cloudflare.com` all connect). This is the production-shaped path, a satellite reaching a remote hub.
- Go dialing maclap's own LAN failed in testing. We confirmed this was a local application firewall (Little Snitch and LuLu) denying an unsigned ad-hoc test binary's LAN connections, not a network or architecture limit. Apple signed tools (nc, curl, ping) were allowed; the real `termyard` binary is an approved app and connects today. See the investigation notes for the signed vs unsigned probe results.

Carry forward rule: data connections must use the same host and port the satellite already reaches the peer on (in a hub deployment, 443). Custom ports and lateral LAN dials are unreliable on managed machines.

## What gets deleted

The multiplexed PTY transport, once the per-terminal path is in:

- `pkg/peer/pty_relay.go` (the stream registry, `DeliverOutput`, `PumpBrowserToPeer`).
- The multiplexed `PTYManager.Open` output path in `pkg/peer/pty_manager.go` (replaced by a per-connection bridge reusing `pty_terminal.go`).
- The PTY frame protocol on the wire (`EncodePTYFrame` / `FramePTYOutput` and the binary demux in `pkg/peer/session.go`).
- The ack and force-redial machinery (`MsgPTYOpenAck`, `WaitForOpenAck`, the ack-timeout goroutine in `handleRemoteSession`). Per connection failure is now local and visible to the browser directly.
- Optionally the hi and lo lanes collapse to a single control queue, since the control connection is no longer contended by PTY output. This is cleanup, not required for correctness.

The trace and pprof instrumentation stays until we have confidence in production, then it is stripped or gated behind a debug flag.

## Tradeoffs

- More connections, roughly one per open terminal. This is exactly what local mode already does without issue.
- Per connection setup cost and a token handshake. Small and one time per terminal.
- The control connection remains a single point for signaling. It is tiny and uncontended, so it is far more robust than today's shared link, but it is still one connection.
- This does not solve the case where both peers are firewalled with no reachable direction. That needs a relay or hub and is out of scope here. The current deployments have a working control-link direction, so the dialer direction is already established.

## Consequences

- Remote terminals use the same reliable one to one transport as local terminals.
- The blank on switch and the firehose starvation are removed by construction, not by tuning.
- The peer protocol shrinks: control plane only, plus a thin per-stream data channel.
- A future hub deployment slots in cleanly, satellites dial the hub on 443 for both control and data connections.
