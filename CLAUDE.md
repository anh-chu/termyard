# CLAUDE.md

## Project Overview

Termyard is a web dashboard for monitoring and interacting with coding agents running in tmux sessions. Go backend + React/Vite frontend embedded in a single binary. The binary name is `termyard`.

## Quick Reference

| Task                  | Command                                      |
| --------------------- | -------------------------------------------- |
| Build everything      | `/usr/bin/make build`                        |
| Build frontend only   | `/usr/bin/make frontend`                     |
| Run dev server        | `/usr/bin/make dev`                          |
| Run Go binary         | `go run . server`                            |
| Frontend dev server   | `cd web && npm run dev`                      |
| Run Go tests          | `go test ./...`                              |
| Clean build artifacts | `/usr/bin/make clean`                        |
| Cut a release         | `./scripts/release.sh [patch\|minor\|major]` |

**Important:** Use `/usr/bin/make` (not `make`) due to a zsh shell function conflict.

## Architecture

- **PTY-based streaming:** Spawns `tmux attach-session` in a PTY per browser connection
- **WebSocket bridge:** Each browser tab gets its own PTY-to-WebSocket bridge
- **Embedded frontend:** Vite builds to `pkg/server/dist/`, Go embeds via `//go:embed`
- **Multi-host:** Star topology with hub/peer model using ed25519 identity and mTLS
- **Agent monitoring:** Coding agents push lifecycle events to `termyard notify` via per-agent hooks; the server tracks state and broadcasts it to the frontend over WebSocket

## Project Structure

```
main.go                  # Entry point, urfave/cli v3
pkg/
  commands/              # CLI commands (server, notify, pair, peers, agent-setup, install)
    agent-setup/         # Per-agent hook installers + embedded plugin/extension sources
    notify/              # `termyard notify` — receives agent hook events, posts to server
  server/                # Chi HTTP server + embedded frontend; POST /api/tool-event
  ws/                    # WebSocket hub (broadcast) + PTY terminal bridge
  state/                 # Central state tree with diff-based broadcasting
  tmux/                  # tmux client, PTY, control mode, discovery
  peer/                  # Multi-host peer protocol, manager, PTY relay
  identity/              # ed25519 keypair, peer store, pairing
  toolevents/            # Agent detection, event tracking, silence monitor, prompt parser
  auth/                  # Password auth + session management
  agentcheck/            # Agent installation & hook configuration checks
  activity/              # Activity tracking (sparklines)
  common/                # Command registration, version, logging setup
  socket/                # Unix socket communication
  preferences/           # User preferences
  stats/                 # System statistics
  tlscert/               # TLS certificate generation
  webpush/               # Browser push notifications
web/
  src/
    App.tsx              # Main app component
    components/          # React components (Sidebar, Terminal, Overview, etc.)
    hooks/               # Custom hooks (useToolEvents, useSessions, useHosts, etc.)
docs/                    # agent-detection, agent-setup, agent-support-matrix, multi-host, ...
scripts/release.sh       # Single source of truth for version bumps
```

## Code Conventions

### Go

- **Module:** `github.com/anh-chu/termyard`
- **CLI framework:** urfave/cli v3
- **Command registration:** Commands register via `init()` calling `common.RegisterCommand()`, imported as blank imports in `main.go`
- **Logging:** logrus (`log.WithField(...)`). Set verbosity with `LOG_LEVEL=trace|debug|info`.
- **HTTP router:** chi v5
- **WebSockets:** gorilla/websocket
- **Environment variables:** Prefixed with `TERMYARD_` (e.g., `TERMYARD_PORT`, `TERMYARD_SOCKET`, `TERMYARD_HUB`)
- **Testing:** Standard `testing` package, table-driven tests with `t.Run()` subtests

### Frontend

- **React 19** with TypeScript (strict mode)
- **Bundler:** Vite 6
- **Styling:** Tailwind CSS 4
- **Terminal:** xterm.js (`@xterm/xterm`)
- **State:** Custom React hooks (no Redux/Zustand)
- **Dev proxy:** Vite proxies to `http://localhost:7654`
- **Build output:** `../pkg/server/dist` (relative to `web/`)

## Tool Events (the agent → termyard contract)

Agents report state by invoking `termyard notify`, which POSTs an `Event` to `POST /api/tool-event` (unauthenticated; auto-stamps host identity). Events drive the sidebar status badges.

**Statuses:** `active` (working), `waiting` (needs user attention), `completed` (turn done), `error`, `stuck`.

**`termyard notify` flags that carry session metadata:**

- `--user-prompt` — the user's first message; **set-once** server-side, and **the task label is derived from it.** There is **no `--task` flag** — it was removed in `18340d5`. Passing `--task` makes the _entire_ notify call fail with `exit 1` ("flag provided but not defined: -task") and silently drop the event. Do not reintroduce it in any agent extension.
- `--agent-message` — the agent's last response; updated each turn.
- `--stdin` — read a hook payload as JSON from stdin (used to map a `tool_name` to an activity label like "running commands").

`completed`/`active` events clear the tracked event for a pane; `waiting`/`error` are retained. Metadata (`user_prompt`, `agent_message`) is applied regardless of status.

## Agent Detection & Status

Detection uses layered signals — see `docs/agent-detection.md` for the full picture. Key rules:

- **Native-hook agents — Claude, OpenCode, Pi** (`nativeWaitingTools` in `pkg/toolevents/tracker.go`) report their own active/waiting/completed lifecycle. **Codex** uses `hooks.json` lifecycle hooks. **Copilot CLI support was removed** (the `ToolCopilot` constant remains only for process-tree detection).
- The **process-tree detector** emits an `auto_detected: true` `active` event when it sees an agent process in a pane with no hook event. This is presence detection, not a real "working" signal.
- The frontend (`Sidebar.tsx`) gates that: an `auto_detected` active event is only shown as "working" when there is **no hook history** (`hasHookHistory = user_prompt || last_agent_message`). With hook history, the process sitting at its REPL between turns reads as **idle**. → If an agent fails to send `user_prompt`/`agent_message`, it gets stuck showing "working" forever.
- The **silence monitor** runs `capture-pane` only on non-Claude panes silent 10+ seconds, max 2 checks per silence period.
- The **inactivity promoter** (30s) and **reconciler** (clears events when the agent process exits) are fallbacks.

## Per-Agent Hook Mechanics

Each agent has its own hook system. Sources live under `pkg/commands/agent-setup/` and are installed by `termyard agent-setup`. See `docs/agent-setup.md`.

- **Claude** — `hooks.json`. `UserPromptSubmit` → `user_prompt`; `Stop` parses the transcript JSONL for the last assistant message; `PreToolUse` → activity label.
- **Codex** — `hooks.json` (separate file). Requires `hooks = true` under `[features]` in `config.toml` (off by default; agent-setup sets it).
- **Pi** — TypeScript extension (`pi-extension/termyard.ts`), enabled in `~/.pi/agent/settings.json`. Pi compiles it via **jiti and caches the result** (`~/.pi/agent/cache/jiti/`). **The extension is loaded once at pi process startup** — editing it requires fully restarting the pi process (not just a new prompt) to take effect. The persistent `pane_pid` is the shell wrapper; the actual pi process is its child. Prompt is captured in `before_agent_start` (the only event carrying it); the agent message is recovered from `agent_end`'s `messages[]` array.
- **OpenCode** — ESM plugin (`opencode-plugin/index.js`) written to `~/.config/opencode/plugins/termyard.js`, which OpenCode auto-loads at startup (the canonical local-files mechanism, no `opencode.json` registration). Export is the v1 `export default { id, server }` shape. agent-setup also cleans up the prior non-canonical install: the `node_modules/termyard` package and its `file://` entry in `opencode.json`.

## Multi-Host & Event Identity

**All tool events must include `Host` and `HostName` fields.** The frontend uses `host` to construct session keys (`host/session`) for URL routing and navigation. Events missing host info fail to navigate to the correct session in multi-host setups.

- The HTTP handler (`POST /api/tool-event`) stamps `Host`/`HostName` from `PeerMgr.LocalID()`/`LocalName()` automatically.
- Components that record events directly (detector, silence monitor, inactivity promoter) must have host identity set via `SetHost()` at startup.
- Frontend session keys: `host/sessionName` for remote sessions, or just `sessionName` for local-only (single host) mode.

## Build & Release

- **Versioning:** `./scripts/release.sh [patch|minor|major]` is the single source of truth. It bumps `pkg/common/version.go`, `web/package.json`, and `.release-please-manifest.json`, commits `chore(release): X.Y.Z`, and pushes `master`. The release-please workflow then tags `vX.Y.Z`.
  - The script does **not** touch `web/package-lock.json`; after a bump, sync its `version` to match and commit `chore: update package-lock to match vX.Y.Z`.
- **GoReleaser** handles multi-platform builds (linux/darwin × amd64/arm64). Pre-hook runs `make frontend`. Binaries are statically linked and stripped. Version/commit injected via ldflags.

## Debugging Agent Events

When an agent's status/metadata is wrong, capture what `notify` actually does rather than theorizing:

1. `LOG_LEVEL=trace` on the server (systemd: drop-in `Environment=LOG_LEVEL=trace`, `daemon-reload`, restart) and watch `journalctl --user -u termyard -f` for `received request` / `recording tool event`.
2. If no events arrive, the failure is in the agent extension or `notify` itself — log the `spawnSync` result (`status`, `stderr`) from inside the extension. A non-zero exit with an "Incorrect Usage" stderr means a bad/removed flag (see the `--task` note above).
