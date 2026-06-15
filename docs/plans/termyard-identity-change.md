# Termyard identity change plan

## Decision

Rename Guppi into Termyard as a new tool, not a backward-compatible continuation.

Target identity:

| Surface           | New value                                   |
| ----------------- | ------------------------------------------- |
| Product name      | Termyard                                    |
| CLI and binary    | `termyard`                                  |
| Repo slug         | `termyard`                                  |
| Go module         | `github.com/anh-chu/termyard`               |
| Env prefix        | `TERMYARD_`                                 |
| Config dir        | `~/.config/termyard`                        |
| Socket            | `termyard.sock`                             |
| Linux units       | `termyard.service`, `termyard-tmux.service` |
| macOS LaunchAgent | `com.termyard.server`                       |
| Frontend package  | `termyard-web`                              |

Compatibility stance:

- No migration helper.
- No fallback reads from `~/.config/guppi`.
- No `GUPPI_*` env aliases.
- No old `guppi` binary alias.
- No changelog update for old history.
- Existing agent hooks must be regenerated with `termyard agent-setup`.

## Implementation sequence

### 1. Rename module and imports

Change:

- `go.mod:1`, `github.com/ekristen/guppi` to `github.com/anh-chu/termyard`.
- All Go imports from `github.com/ekristen/guppi/pkg/...` to `github.com/anh-chu/termyard/pkg/...`.
- Representative files:
  - `main.go:11`, `main.go:13-17`
  - `pkg/commands/server/server.go`
  - `pkg/commands/notify/notify.go`
  - `pkg/commands/agent-setup/agent_setup.go`
  - `pkg/state/manager.go`
  - `pkg/peer/*`

Use `gofmt` after edits.

### 2. Rename binary, version name, build and release artifacts

Change:

| File                      | Change                                  |
| ------------------------- | --------------------------------------- |
| `pkg/common/version.go:4` | `NAME = "guppi"` to `NAME = "termyard"` |
| `Makefile:5`              | `dist/guppi` to `dist/termyard`         |
| `.gitignore:23`           | `/guppi` to `/termyard`                 |
| `.goreleaser.yml:5`       | `name: guppi` to `name: termyard`       |
| `.goreleaser.yml:14`      | build id `guppi` to `termyard`          |
| `.goreleaser.yml:35`      | archive id `guppi` to `termyard`        |

Check archive naming. `.goreleaser.yml` uses `{{ .ProjectName }}`, so release output should become `termyard` once project metadata changes. Verify with GoReleaser dry run.

### 3. Rename runtime config, sockets and env vars

Change all runtime prefixes from Guppi to Termyard.

Env vars:

| Old                        | New                           | Representative file refs                                                           |
| -------------------------- | ----------------------------- | ---------------------------------------------------------------------------------- |
| `GUPPI_PORT`               | `TERMYARD_PORT`               | `pkg/commands/server/server.go:304`                                                |
| `GUPPI_DISCOVERY_INTERVAL` | `TERMYARD_DISCOVERY_INTERVAL` | `pkg/commands/server/server.go:310`                                                |
| `GUPPI_NO_CONTROL_MODE`    | `TERMYARD_NO_CONTROL_MODE`    | `pkg/commands/server/server.go:316`                                                |
| `GUPPI_SOCKET`             | `TERMYARD_SOCKET`             | `pkg/commands/server/server.go:321`, `pkg/commands/notify/notify.go:647`           |
| `GUPPI_NO_AUTH`            | `TERMYARD_NO_AUTH`            | `pkg/commands/server/server.go:326`                                                |
| `GUPPI_NO_RECOVERY`        | `TERMYARD_NO_RECOVERY`        | `pkg/commands/server/server.go:331`                                                |
| `GUPPI_URL`                | `TERMYARD_URL`                | `pkg/commands/notify/notify.go:641`, `pkg/commands/agent-setup/agent_setup.go:706` |
| `GUPPI_UPDATE_CHANNEL`     | `TERMYARD_UPDATE_CHANNEL`     | `pkg/commands/update/update.go:436`                                                |
| `GUPPI_UPDATE_REPO`        | `TERMYARD_UPDATE_REPO`        | `pkg/commands/update/update.go:445`                                                |
| `GUPPI_NAMER_*`            | `TERMYARD_NAMER_*`            | `pkg/namer/namer.go:78-80`, `pkg/preferences/preferences.go`                       |
| `GUPPI_OPENAI_*`           | `TERMYARD_OPENAI_*`           | `pkg/namer/namer.go`, `pkg/preferences/preferences.go`                             |

Config paths:

| Old                                  | New                                     | Representative file refs             |
| ------------------------------------ | --------------------------------------- | ------------------------------------ |
| `~/.config/guppi/identity.json`      | `~/.config/termyard/identity.json`      | `pkg/identity/identity.go:85`        |
| `~/.config/guppi/peers.json`         | `~/.config/termyard/peers.json`         | `pkg/identity/peers.go:48`           |
| `~/.config/guppi/auth.json`          | `~/.config/termyard/auth.json`          | `pkg/auth/auth.go:35`                |
| `~/.config/guppi/preferences.json`   | `~/.config/termyard/preferences.json`   | `pkg/preferences/preferences.go:109` |
| `~/.config/guppi/schedules.json`     | `~/.config/termyard/schedules.json`     | `pkg/scheduler/store.go:47`          |
| `~/.config/guppi/session-names.json` | `~/.config/termyard/session-names.json` | `pkg/state/manager.go:75`            |

Socket:

- `pkg/socket/socket.go:10`, `guppi.sock` to `termyard.sock`.
- `pkg/socket/socket.go` comments/examples, `guppi` path pieces to `termyard`.

### 4. Rename update flow

Change update code because it downloads repo assets by name.

| File ref                                            | Change                                                                           |
| --------------------------------------------------- | -------------------------------------------------------------------------------- |
| `pkg/commands/update/update.go:29`                  | `anh-chu/guppi` to `anh-chu/termyard`                                            |
| `pkg/commands/update/update.go:88`, `:176`          | `guppi-update/` user agent to `termyard-update/`                                 |
| `pkg/commands/update/update.go:143`                 | archive pattern `guppi-v%s-%s-%s.tar.gz` to `termyard-v%s-%s-%s.tar.gz`          |
| `pkg/commands/update/update.go:223`, `:246`, `:262` | binary extraction checks from `guppi`, `guppi.exe` to `termyard`, `termyard.exe` |
| `pkg/commands/update/update.go:270`, `:275-276`     | service checks from `guppi.service` to `termyard.service`                        |

Rename helper symbols too, for example `extractGuppiBinary` to `extractTermyardBinary`.

### 5. Rename install services

Change generated install units and user-facing messages in `pkg/commands/install/install.go`.

| Old                                       | New                                          |
| ----------------------------------------- | -------------------------------------------- |
| `Guppi - Web dashboard for tmux sessions` | `Termyard - Web dashboard for tmux sessions` |
| `guppi.service`                           | `termyard.service`                           |
| `guppi-tmux.service`                      | `termyard-tmux.service`                      |
| `com.guppi.server`                        | `com.termyard.server`                        |
| `com.guppi.server.plist`                  | `com.termyard.server.plist`                  |
| `GUPPI_URL`                               | `TERMYARD_URL`                               |

Representative refs:

- `pkg/commands/install/install.go:18-20`
- `pkg/commands/install/install.go:55`
- `pkg/commands/install/install.go:114-115`
- `pkg/commands/install/install.go:156`, `:159`, `:184`, `:221`, `:223`, `:274`

No old service cleanup. User reinstalls as Termyard.

### 6. Rename agent setup and embedded hooks

Agent setup writes config into Claude, Codex, OpenCode and Pi. Change embedded templates and generated paths.

| Surface             | Old                                              | New                      | Representative refs                              |
| ------------------- | ------------------------------------------------ | ------------------------ | ------------------------------------------------ |
| Pi extension source | `pkg/commands/agent-setup/pi-extension/guppi.ts` | `termyard.ts`            | source file path and `agent_setup.go:648`        |
| Pi extension env    | `GUPPI_BIN`                                      | `TERMYARD_BIN`           | `pi-extension/guppi.ts:4`                        |
| Template token      | `__GUPPI_BIN__`                                  | `__TERMYARD_BIN__`       | `agent_setup.go:19-20`, `:539-542`, `:646`       |
| Pi settings entry   | `extensions/guppi.ts`                            | `extensions/termyard.ts` | `agent_setup.go:631`                             |
| OpenCode plugin     | `guppi.js`                                       | `termyard.js`            | `agent_setup.go:502`, `:510`                     |
| OpenCode token      | `__GUPPI_BIN__`                                  | `__TERMYARD_BIN__`       | `opencode-plugin/index.js:1`                     |
| Detection helper    | `isGuppi`                                        | `isTermyard`             | `agent_setup.go:572`                             |
| Agent checker paths | `guppi.json`, `guppi.js`, `guppi.ts`             | `termyard.*`             | `pkg/agentcheck/agentcheck.go:88`, `:96`, `:102` |

Installed user files after setup:

- `~/.pi/agent/extensions/termyard.ts`
- `~/.config/opencode/plugins/termyard.js`
- `~/.claude/settings.json` hook commands using `termyard notify`
- `~/.codex/config.toml` and `~/.codex/hooks.json` hook commands using `termyard notify`

Existing old hooks break by design. Users rerun:

```bash
termyard agent-setup
```

### 7. Rename notify command messages

Change user-facing notify strings.

| File ref                            | Old                                | New                                   |
| ----------------------------------- | ---------------------------------- | ------------------------------------- |
| `pkg/commands/notify/notify.go:570` | `failed to notify guppi`           | `failed to notify termyard`           |
| `pkg/commands/notify/notify.go:577` | `guppi returned status`            | `termyard returned status`            |
| `pkg/commands/notify/notify.go:654` | `Notify guppi of AI tool activity` | `Notify termyard of AI tool activity` |

Keep tool names in `-t claude`, `-t codex`, `-t opencode`, `-t pi` unchanged. Those identify agent type, not product identity.

### 8. Rename frontend package, UI copy and browser storage

Package and metadata:

| File                     | Change                                                                 |
| ------------------------ | ---------------------------------------------------------------------- |
| `web/package.json:2`     | `guppi-web` to `termyard-web`                                          |
| `web/package-lock.json`  | refresh after package rename                                           |
| `web/index.html:8`       | `content="guppi"` to `content="termyard"`                              |
| `web/index.html:15`      | `<title>Guppi</title>` to `<title>Termyard</title>`                    |
| `web/public/favicon.svg` | SVG label `Guppi` to `Termyard`, or replace later with chosen new logo |

UI copy refs:

- `web/src/App.tsx:943`, document title `Guppi` to `Termyard`.
- `web/src/components/Login.tsx:44`, `alt="guppi"` to `alt="termyard"`.
- `web/src/components/TopBar.tsx:79`, `alt="guppi"` to `alt="termyard"`.
- `web/src/components/Settings.tsx:772`, `guppi machines` to `termyard machines`.
- `web/src/components/PortForwardModal.tsx:12`, `:87`, `through guppi` to `through termyard`.
- `web/src/components/Overview.tsx:27`, `guppi_mem_mb` to `termyard_mem_mb` if backend also changes stat key.

Browser storage and DnD namespaces:

- `guppi:*` localStorage keys to `termyard:*` in `web/src/App.tsx` and `web/src/components/Sidebar.tsx`.
- `application/x-guppi-pane` to `application/x-termyard-pane`.
- `application/x-guppi-new-session` to `application/x-termyard-new-session`.

No localStorage migration. Old browser UI state is orphaned by design.

### 9. Rename docs, scripts and repo metadata

Update current docs and examples, excluding `CHANGELOG.md`.

Key refs:

| File                              | Change                                                                                   |
| --------------------------------- | ---------------------------------------------------------------------------------------- |
| `README.md:1`                     | `# GUPPI` to `# Termyard`                                                                |
| `README.md:36`, `:39`             | `dist install ekristen/guppi` to `dist install anh-chu/termyard`                         |
| `README.md:45`                    | `https://get.guppi.sh` to `https://get.termyard.sh`                                      |
| `README.md:55`                    | `github.com/ekristen/guppi.git` to `github.com/anh-chu/termyard.git`                     |
| `README.md:56`                    | `cd guppi` to `cd termyard`                                                              |
| `README.md:58`                    | `dist/guppi` to `dist/termyard`                                                          |
| `README.md:231`                   | `~/.config/guppi/peers.json` to `~/.config/termyard/peers.json`                          |
| `docs/agent-setup.md`             | command examples and product name                                                        |
| `docs/agent-detection.md`         | product name and hook references                                                         |
| `docs/multi-host.md`              | config paths, env vars, service names                                                    |
| `docs/tmux-setup.md`              | env vars and CLI examples                                                                |
| `docs/theme.md`                   | product name if present                                                                  |
| `docs/plans/symmetric-peering.md` | either update or archive as historical plan, but do not leave current instructions stale |
| `CLAUDE.md`                       | project overview and command examples                                                    |
| `TODO.md`                         | task text only if still relevant                                                         |

Do not update `CHANGELOG.md` unless user later asks for historical rewrite.

External tasks outside repo:

- Rename GitHub repo to `anh-chu/termyard`.
- Configure GoReleaser secrets against new repo if needed.
- Set up `get.termyard.sh` DNS and hosting, if keeping curl installer.
- Update package listings or gallery entries outside repo.

## Generated and embedded files

Do not edit generated outputs directly:

- `pkg/server/dist/**`, generated by Vite from `web/`.
- `pkg/server/embed.go`, embeds `dist/*` and should not need rename edits.
- `web/node_modules/**`.
- Release artifacts under `dist/**`.

Commit source changes, then regenerate:

- `web/package-lock.json`, via npm install or lockfile update after `web/package.json` rename.
- `pkg/server/dist/**`, via frontend build.

Embedded hook sources are real source files and should be edited:

- `pkg/commands/agent-setup/pi-extension/guppi.ts`, likely renamed to `termyard.ts`.
- `pkg/commands/agent-setup/opencode-plugin/index.js`.

## Risks

| Risk                                                          | Decision or mitigation                                                            |
| ------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| Existing configs under `~/.config/guppi` stop being used      | Accepted. New tool birth. User can manually copy files if desired.                |
| Existing `GUPPI_*` env vars stop working                      | Accepted. Docs use only `TERMYARD_*`.                                             |
| Existing Claude/Codex/OpenCode/Pi hooks call old `guppi` path | Accepted. User reruns `termyard agent-setup`.                                     |
| Existing systemd or launchd services keep old names           | Accepted. User reinstalls Termyard service. No cleanup helper.                    |
| Update command downloads wrong repo or asset                  | Must update repo slug, archive names and binary extraction checks in same change. |
| Frontend localStorage starts fresh                            | Accepted. Old `guppi:*` keys orphaned.                                            |
| Generated frontend output gets stale                          | Run frontend build before final Go build.                                         |
| `CHANGELOG.md` contains old links                             | Accepted. No changelog work.                                                      |
| Install script domain does not exist yet                      | External launch task. README may point to future `get.termyard.sh`.               |

## Verification checklist

Run from repo root.

```bash
# Module name
grep '^module ' go.mod
# expected: module github.com/anh-chu/termyard

# No old import path outside changelog
rg 'github.com/ekristen/guppi' . --glob '!CHANGELOG.md' --glob '!web/node_modules' --glob '!pkg/server/dist' --glob '!dist'

# No old env prefix in source/docs except changelog if any
rg 'GUPPI_' . --glob '!CHANGELOG.md' --glob '!web/node_modules' --glob '!pkg/server/dist' --glob '!dist'

# No old config path in current files
rg '\.config/guppi|/guppi/|guppi\.sock' . --glob '!CHANGELOG.md' --glob '!web/node_modules' --glob '!pkg/server/dist' --glob '!dist'

# No old service names
rg 'com\.guppi|guppi\.service|guppi-tmux\.service' . --glob '!CHANGELOG.md' --glob '!web/node_modules' --glob '!pkg/server/dist' --glob '!dist'

# No old frontend namespaces
rg 'guppi:|x-guppi|guppi_mem_mb|guppi-web' web pkg --glob '!web/node_modules' --glob '!pkg/server/dist'

# No user-facing old product name, excluding changelog and generated assets
rg '\bGuppi\b|\bGUPPI\b|\bguppi\b' . --glob '!CHANGELOG.md' --glob '!web/node_modules' --glob '!pkg/server/dist' --glob '!dist' --glob '!docs/brand/termyard-logo-new-directions.html'

# Go formatting and tests
gofmt -w $(find . -name '*.go' -not -path './web/*')
go test ./...

# Frontend package and build
cd web && npm install && npm run build && cd ..

# Full build, use project-required make path
/usr/bin/make build

# Binary exists and reports new name
./dist/termyard --version
./dist/termyard --help | head -40

# GoReleaser config sanity, if goreleaser installed
goreleaser check
```

Expected final stale refs:

- `CHANGELOG.md` may keep old historical refs.
- Git metadata under `.git` ignored.
- Fresh generated frontend files should contain Termyard after `npm run build`.
