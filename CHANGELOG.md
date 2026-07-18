# Changelog

## [4.0.0] — Breaking Changes

### ⚠ BREAKING CHANGES

- **backend:** tmux is no longer required. The daemon PTY backend is now the only session backend. All sessions run as independent daemon processes that survive server crashes, restarts, and OOM events.

### Features

- **daemon:** daemon is now the default and only backend for all sessions
- **daemon:** sessions survive server crashes — each session runs as an independent process with its own process group (`Setsid`)
- **daemon:** automatic session rediscovery on server restart via socket directory scanning
- **daemon:** ring buffer replay — reconnecting clients receive the last 1MB of terminal output

### Session Reliability

- **registry:** verify daemon process is alive (PID check via `/proc`) before removing stale sockets — prevents accidentally orphaning running sessions
- **registry:** increase liveness failure threshold from 3 to 5 consecutive failures for more tolerance under load
- **registry:** mass-removal protection — if all sessions appear stale simultaneously, skip cleanup (likely transient system event)
- **state:** mass-removal guard — refuse to remove >50% of tracked sessions in one update cycle
- **state:** refuse to clear all sessions when discovery returns empty (likely transient failure)
- **daemon:** panic recovery in all daemon goroutines (`pumpPTY`, `handleClient`, accept loop) — a panic in one client connection doesn't crash the daemon
- **daemon:** cap DaemonSession client buffer at 4MB to prevent server OOM from output flooding

### Removed

- **tmux:** removed tmux dependency entirely (2,794 lines deleted)
- **tmux:** removed tmux control mode, session creation, and all tmux-specific code paths
- **recovery:** removed tmux session rebuilder (no longer needed — daemon sessions persist independently)

## [2.2.2](https://github.com/anh-chu/termyard/compare/v2.2.1...v2.2.2)

### Bug Fixes

- **namer:** make the AI-name button work for remote peer sessions. The name is now generated on the hub (using the remote session's prompt, agent message, project, and sibling names) and sent to the peer to apply, so it no longer silently no-ops when the peer process has no namer configured

## [2.2.1](https://github.com/anh-chu/termyard/compare/v2.2.0...v2.2.1)

### Bug Fixes

- **namer:** wire distinct names + latest user prompt into the manual regenerate button, which still used the first prompt and ignored sibling names

## [2.2.0](https://github.com/anh-chu/termyard/compare/v2.1.1...v2.2.0)

### Features

- **namer:** make AI session names distinct and current — feed sibling session names into the prompt so labels differ by wording instead of numeric suffixes, name by what differs when sessions share a project/branch/agent, use the latest user prompt for naming (the sidebar still shows the first), re-name on a fresh user prompt, and give reasoning models token headroom by taking the final output line

## [1.3.0](https://github.com/anh-chu/termyard/compare/v1.2.1...v1.3.0)

### Performance

- **peer:** make remote sessions hyper-performant — split the control channel into hi/lo priority lanes so bulky state snapshots never block keystroke echoes, ship PTY data as raw binary frames (no base64/JSON per chunk), move marshaling off the single writer, deepen the interactive queue, and raise WebSocket buffers to 32KB. Eliminates typing latency, jitter, and head-of-line blocking on remote peer sessions.

## [1.2.0](https://github.com/anh-chu/termyard/compare/v1.1.0...v1.2.0)

### Features

- **terminal:** add opt-in coding ligature support (Fira Code / JetBrains Mono) via `@xterm/addon-ligatures`, gated behind a Settings → Terminal toggle (default off)

## [0.5.0](https://github.com/ekristen/guppi/compare/v0.4.0...v0.5.0) (2026-06-13)

### Bug Fixes

- **sidebar:** use !important to ensure selected session text color overrides base ([fbfada9](https://github.com/ekristen/guppi/commit/fbfada9))

## [0.1.1-beta.2](https://github.com/ekristen/guppi/compare/v0.1.0-beta.2...v0.1.1-beta.2) (2026-03-15)

### Features

- better font/size ([a607c16](https://github.com/ekristen/guppi/commit/a607c162761eac26e2dec4eaebf637d07b0cca61))
- better font/size ([a5cf00b](https://github.com/ekristen/guppi/commit/a5cf00bc68d50fd4d78fb121d8c2520210df6f77))
