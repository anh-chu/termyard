# guppi — Overview

> **Navigation aid.** This article shows WHERE things live (routes, models, files). Read actual source files before implementing new features or making changes.

**guppi** is a go project built with chi.

## Scale

87 API routes · 15 UI components · 61 library files · 1 middleware layers · 7 environment variables

## Subsystems

- **[Auth](./auth.md)** — 10 routes — touches: auth, db, queue, ai
- **[Payments](./payments.md)** — 2 routes — touches: auth, db, queue, ai
- **[*](./section.md)** — 1 routes — touches: auth, db, queue, ai
- **[Content-Encoding](./content-encoding.md)** — 1 routes — touches: auth, db, queue, ai
- **[Content-Type](./content-type.md)** — 1 routes — touches: auth, db, queue, ai
- **[Upgrade](./upgrade.md)** — 1 routes — touches: auth, db, queue, ai
- **[Activity](./activity.md)** — 2 routes — touches: auth, db, queue, ai
- **[Agent-status](./agent-status.md)** — 2 routes — touches: auth, db, queue, ai
- **[Cols](./cols.md)** — 1 routes — touches: auth, db, queue, ai
- **[Host](./host.md)** — 1 routes — touches: auth, db, queue, ai
- **[Hosts](./hosts.md)** — 2 routes — touches: auth, db, queue, ai
- **[Hub](./hub.md)** — 1 routes — touches: auth
- **[Layout](./layout.md)** — 4 routes — touches: auth, db, queue, ai
- **[Name](./name.md)** — 1 routes — touches: auth, db, queue, ai
- **[Notify](./notify.md)** — 1 routes — touches: auth, ai
- **[Peers](./peers.md)** — 11 routes — touches: auth, db, queue, ai
- **[Peers_test](./peers_test.md)** — 1 routes — touches: auth, db
- **[Portforward](./portforward.md)** — 2 routes — touches: auth, db, queue, ai
- **[Portforwards](./portforwards.md)** — 4 routes — touches: auth, db, queue, ai
- **[Preferences](./preferences.md)** — 4 routes — touches: auth, db, queue, ai
- **[Proxy](./proxy.md)** — 2 routes — touches: auth, db, queue, ai
- **[Push](./push.md)** — 4 routes — touches: auth, db, queue, ai
- **[Rows](./rows.md)** — 1 routes — touches: auth, db, queue, ai
- **[Session](./session.md)** — 10 routes — touches: auth, db, queue, ai
- **[Sessions](./sessions.md)** — 2 routes — touches: auth, db, queue, ai
- **[Stats](./stats.md)** — 2 routes — touches: auth, db, queue, ai
- **[Tool-event](./tool-event.md)** — 4 routes — touches: auth, db, queue, ai
- **[Tool-events](./tool-events.md)** — 4 routes — touches: auth, db, queue, ai
- **[Version](./version.md)** — 2 routes — touches: auth, db, queue, ai
- **[Ws](./ws.md)** — 3 routes — touches: auth, db, queue, ai

**UI:** 15 components (react) — see [ui.md](./ui.md)

**Libraries:** 61 files — see [libraries.md](./libraries.md)

## High-Impact Files

Changes to these files have the widest blast radius across the codebase:

- `encoding/json` — imported by **22** files
- `path/filepath` — imported by **21** files
- `net/http` — imported by **11** files
- `os/exec` — imported by **10** files
- `web/src/lib/utils.ts` — imported by **10** files
- `web/src/hooks/usePreferences.ts` — imported by **9** files

## Required Environment Variables

- `GUPPI_BIN` — `pkg/commands/agent-setup/pi-extension/guppi.ts`
- `PATH` — `pkg/commands/install/install.go`
- `SHELL` — `pkg/tmux/client.go`
- `TMPDIR` — `pkg/socket/socket.go`
- `TMUX_PANE` — `pkg/commands/notify/notify.go`
- `XDG_DATA_HOME` — `pkg/webpush/vapid.go`
- `XDG_RUNTIME_DIR` — `pkg/socket/socket.go`

---
_Back to [index.md](./index.md) · Generated 2026-06-01_