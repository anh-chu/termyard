# Changelog

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
