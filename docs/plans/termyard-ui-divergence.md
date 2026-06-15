# Termyard UI Divergence Plan

Status: proposed
Owner: (assign)
Scope: `web/` frontend only. No backend/API changes required — every data
dependency already exists (`/api/stats`, `/api/tool-events`, `/api/activity`,
sessions WS).

## 1. Why

After the Guppi→Termyard rebrand the UI is structurally unchanged from Guppi: a
generic dark "developer telemetry dashboard" (Raycast-clone theme, ALL-CAPS
micro-labels, `tracking-widest`, dense stat-cards, a footer dumping ~12
`LABEL: value` readouts). It looks like every other monitor tool.

Termyard already has a brand the UI ignores: the logo is _a yard of parked tmux
panes, one lit up when it needs you_. This plan bends the app to that idea so it
reads as **a yard you watch** — calm by default, loud only when an agent needs
you — instead of a busy SaaS dashboard.

Decision (from the design discussion):

- **Theme reskin (option A) is explicitly OUT of scope here.** Re-coloring is a
  small piece; the app would still be structurally Guppi. We diverge by
  structure and behavior, keeping the change theme-agnostic.
- **In scope:** B (Yard dashboard), C (signal-first status), D (texture + type
  identity), plus reworks of header, footer, and settings.

## 2. Design principles

1. **Calm by default.** Nothing animates, pulses, or shouts unless it needs
   you. Idle/healthy state is quiet, low-contrast, still.
2. **One signal.** "Needs you" is the only loud state and it always looks the
   same everywhere (header, footer, yard, sidebar). Attention is scarce and
   literal.
3. **The yard is the product.** The primary surface shows sessions as parked
   panes; the lit one _is_ the logo, alive.
4. **Physical, not telemetry.** Prefer ambient indicators (a glow, a fill, a
   dot) over numeric readouts. Numbers only where a number is the answer.
5. **Texture as connective tissue.** A subtle pane-grid hairline + a distinct
   display type pairing tie the surfaces together — quiet, never decorative
   noise.

## 3. Shared primitive: the signal-first status model (Phase 0)

Everything else depends on one shared notion of "what state is this session in,
and how loud should it be." Build this first.

### 3.1 New file: `web/src/lib/sessionState.ts`

A single function that collapses the current scattered logic
(`isSessionActive`, `hasWaiting`, `statusConfig`, tool-event status) into one
ranked state per session. Inputs already available in `Overview`/`App`.

```ts
import { Session } from "../hooks/useSessions";
import { ToolEvent } from "../hooks/useToolEvents";
import { ActivitySnapshot } from "../hooks/useActivity";

// Ranked loudest → quietest. Only NEEDS_YOU is "loud".
export type SessionState =
  | "needs_you" // waiting | stuck | error  → the ONE loud state
  | "working" // agent in an active turn / live output
  | "idle" // session exists, agent present, nothing happening
  | "offline"; // host offline

export interface SessionSignal {
  state: SessionState;
  loud: boolean; // exactly === (state === 'needs_you')
  reason?: string; // e.g. "waiting", "error" — drives tooltip/label
  tool?: string; // dominant tool for accenting
  agentCount: number;
}

export function sessionSignal(
  session: Session,
  events: ToolEvent[],
  activity: ActivitySnapshot | undefined,
  inActiveTurn: boolean,
): SessionSignal {
  /* ... */
}
```

Rules (derive from existing code in `Overview.tsx` + `useToolEvents`):

- `offline` → `session.host && session.host_online === false`.
- `needs_you` → any event with `status` in `waiting | stuck | error`.
  `reason` = that status. **This is the only `loud: true` case.**
- `working` → `inActiveTurn` (from `isSessionInActiveTurn`) OR `isSessionActive`
  (non-shell `current_command`) OR `activity.idle_seconds` small.
- `idle` → otherwise.

`agentCount` reuses the existing reducer in `Overview` (panes whose
`current_command` is an agent command, or whose `id` is in an event pane set).

### 3.2 Status → visual treatment (the C contract)

Define ONE mapping used by every surface. Add to `theme.ts` (or a new
`signal.ts`) — **theme-agnostic**, built from existing CSS vars so it works in
all presets without the option-A reskin:

| state     | dot / glow             | text          | motion          |
| --------- | ---------------------- | ------------- | --------------- |
| needs_you | `var(--warning)` solid | `--warning`   | gentle pulse    |
| working   | `var(--success)` soft  | `--body-text` | none (or faint) |
| idle      | `var(--mute)` hollow   | `--mute`      | none            |
| offline   | `var(--stone)` hollow  | `--mute/50`   | none            |

Notes:

- We deliberately reuse `--warning` as "the signal" rather than introducing the
  brand lime, to stay decoupled from option A. A later theme pass can remap
  `--warning`→lime per-theme without touching component code.
- Today FIVE statuses each get a distinct loud color (`statusConfig`). After
  this, only `needs_you` is loud; `active/completed` stop competing for
  attention. `statusConfig` stays for the alert _detail_ (label text) but its
  colors are no longer painted everywhere.

Acceptance (Phase 0): unit test `sessionState.test.ts` covering each branch
(mirror `prune.test.ts` style). No visual change yet.

## 4. Phase 1 — Header: marquee, not toolbar

File: `web/src/components/TopBar.tsx` (currently 249 lines).

### Current

44px bar crammed with: logo, new-session/split/collapse icons, separator,
inline alert pills, CLEAR, connection dot+label, port-forwards icon, schedules
icon, settings icon.

### Target

Quiet by default; the alert is the only thing that may dominate.

- **Left:** logo (unchanged) + a **glance strip** — a single calm line built
  from `sessionSignal` counts:
  `▢ 3 parked · ◴ 2 working · ● 1 waiting`
  Hide any zero segment. The "waiting" segment uses the loud treatment; the
  rest are `--mute`. This replaces the always-present icon cluster as the
  primary left content.
- **Center/flex:** when `actionable.length > 0`, the alert region **expands to
  fill the bar** in the loud treatment (it currently shares space as small
  pills). One primary alert shown large + clickable (jump-to-session); extras
  collapse to `+N` that opens the existing list. When `actionable.length === 0`
  show nothing here (drop the literal `NO ALERTS` text — absence is the signal).
- **Right:** collapse to a single **overflow / command button** (`⋯` or a
  command-bar entry) that houses port-forwards, schedules, new-session,
  split-pane. Keep only: connection status (reduced to a bare ambient dot, no
  `ONLINE/OFFLINE/CONNECTING` word unless not-connected) and settings gear.

### Implementation notes

- New prop: pass `signals` (array/Map of `SessionSignal`) or compute counts in
  `App` and pass `glance={{parked, working, waiting}}`. `App` already has
  `sessions`, `getSessionEvents`, `getSessionActivity`, `isSessionInActiveTurn`.
- Move the secondary action buttons into a small `HeaderOverflow` subcomponent
  (or reuse `QuickSwitcher` pattern) — props (`onNewSession`, `onPortForwards`,
  `onSchedules`, `onSplitPane`) just relocate, no logic change.
- Keep the auto-dismiss timer effect as-is.

Acceptance: with zero alerts the header is visually near-empty except logo +
glance strip + connection dot + gear. With ≥1 alert the bar is dominated by one
loud alert. No feature lost (all actions reachable via overflow).

## 5. Phase 2 — Footer: ambient rail, not readout dump

File: `web/src/components/StatusBar.tsx` (currently 135 lines).

### Current

`flex justify-between` footer with ~12 `LABEL: value` spans: PEERS, HOSTS,
SESSIONS, AGENTS, SESSION, WIN, PANES, WAITING, CPU, MEM, PUSH, CONNECTED,
version, help.

### Target

Three glanceable zones, ambient over numeric. Carries the texture (Phase 5).

- **Left — yard pulse:** one combined ambient indicator:
  `agents needing you` count, shown in the loud treatment ONLY when > 0 (hidden
  at 0). Plus a tiny non-numeric session/agent presence (e.g. a row of dots or
  `5 sessions · 2 agents` in `--mute`). Drop WIN/PANES/SESSION — they are
  redundant with the active view and the sidebar.
- **Center — system pulse:** replace `CPU: 14%  MEM: 38%` text with a compact
  dual micro-bar or sparkline (reuse `Sparkline`/`UsageBar` styling), no labels;
  tooltip on hover for exact numbers. Polls `/api/stats` as today.
- **Right — connection + version + help:** connection reduced to ambient dot
  (word only when disconnected/connecting). Keep version + update-available
  affordance and the `?` help. PEERS/HOSTS only shown when `hosts.length > 1`,
  and as dots not words.

### Implementation notes

- Same `/api/stats` poll; same props. Mostly a markup/treatment rewrite.
- Reuse `sessionSignal` waiting-count instead of the passed `waitingCount` if
  convenient, or keep `waitingCount` prop.
- Apply the Phase-5 hairline-grid background here first (smallest surface).

Acceptance: footer shows ≤ 4 elements at a glance; numbers are on-hover, not
always-on; "needs you" is the only colored thing when something waits.

## 6. Phase 3 — The Yard (Overview rework)

File: `web/src/components/Overview.tsx` (currently 494 lines). Biggest impact.

### Current

Top: 6 stat-cards (Hosts/Sessions/Windows/Panes/Agents/Waiting). Then pending
alerts list. Then per-host session cards in an `auto-fill minmax(340px)` grid
(name, attached badge, uptime, window/agent/active counts, optional sparkline).
Then per-host Processes + System stats cards.

### Target: a yard map

Sessions become **parked panes** in a calm grid; state drives prominence.

- **Drop the 6 stat-cards entirely.** Their numbers move to the header glance
  strip (counts) and footer (system). The yard itself shows the truth.
- **Session tile → "pane in the yard":**
  - Default (idle): low-contrast tile, hollow dot, name in `--mute`, still.
  - working: faint live accent (success-soft), subtle activity (the existing
    `Sparkline` shown small) — but no pulsing.
  - **needs_you: the lit pane.** Loud treatment — `--warning` border/glow,
    gentle pulse, the reason ("waiting"/"stuck"/"error") + tool + message
    inline, click jumps to session/pane. This is the logo, alive.
  - offline: dimmed, hollow.
  - Sort order within a group: `needs_you` first, then `working`, then `idle`,
    then `offline` (use `sessionSignal.state` rank). So the lit pane rises.
- **Layout echoes tmux panes:** keep a grid but tighten the visual into a
  pane-grid (hairline gutters, square-ish cells) rather than rounded SaaS cards,
  reinforcing "yard of panes" + Phase-5 texture. Empty/low-session states show
  faint grid cells (the empty yard).
- **Pending-alerts block: remove as a separate section.** It is now redundant —
  the lit tiles ARE the alerts, sorted to the top, and the header carries the
  overflow. (Keeps one source of truth for "needs you".)
- **System / per-host stats:** demote. Move CPU/MEM/load/processes off the main
  yard. Options (pick during impl): (a) a collapsible "yard health" strip at the
  bottom, default collapsed; or (b) into the Settings/Network drawer; or (c) a
  small hover-popover from the footer system pulse. Recommended: (a) collapsed
  strip — keeps the yard clean but one click away. `ProcessBar`,
  `SystemStatsCard`, `HostStatsSection`, `UsageBar` are reused there.

### Implementation notes

- Reuse existing per-session derivation (events, activity, agentCount) but route
  it through `sessionSignal` so prominence + sort are consistent with header
  and footer.
- Grouping by host stays (`hasMultipleHosts`), restyled as yard zones.
- `prefs.sparklines_visible` still gates the working-state sparkline.

Acceptance: open with all-idle sessions → calm, monochrome yard, nothing moving.
Trigger a `waiting` event → exactly one tile lights, rises to top, matches the
header alert and footer count. No stat-cards. System stats reachable but not in
the way.

## 7. Phase 4 — Settings: control cabinet (drawer + regroup)

File: `web/src/components/Settings.tsx` (currently 824 lines). Today: full-page
route (`/settings`), one long scroll, nav pills, 9 sections
(`appearance, terminal, interface, naming, shortcuts, notifications, agents,
peers, security`), ALL-CAPS section headers.

### Target

- **Right-side drawer over the yard** instead of a full-page route, so
  theme/font/texture/naming changes preview live against the yard behind it.
  (Keep the `/settings` URL working — open the drawer when path is `/settings`;
  this is a presentation change, the section content stays.)
- **Two-pane inside the drawer:** left rail = category list, right = content.
  Replaces the scroll + floating pills.
- **Regroup 9 → 4 user-mental-model buckets** (pure relabel/reorder, same
  controls):
  - **Look** ← appearance + terminal font/size + the new texture/type toggles
  - **Yard** ← interface (default view, sidebar) + naming + shortcuts
  - **Alerts** ← notifications + agents (banner/auto-dismiss/push)
  - **Network** ← peers + security (+ optional yard-health/system stats home)
- **Strip ALL-CAPS noise:** section headers and row labels move to sentence
  case / calm weight (keep `Kbd`, toggles, inputs as-is functionally).
- **Live preview:** because the drawer floats over the yard, changing theme,
  font, timestamp format, or texture updates the visible yard immediately
  (already true for theme via `applyTheme`; ensure texture/type vars apply the
  same way).

### Implementation notes

- Wrap existing `Section`/`Row`/inputs in a `SettingsDrawer` shell; the 4
  buckets are just grouped renders of the existing `sectionIds` content.
- `App.tsx` `currentView === 'settings'` becomes "drawer open" rather than a
  full-screen swap; the yard stays mounted behind it.
- No preference schema changes except the two new Phase-5 prefs below.

Acceptance: settings opens as a drawer; yard visible + live-updating behind it;
9 sections reachable under 4 labels; no ALL-CAPS headers.

## 8. Phase 5 — Texture + type identity (D)

The connective tissue. Theme-agnostic (no palette change).

- **Pane-grid hairline texture:** a reusable CSS utility / background
  (`--hairline-soft` lines on a subtle grid) applied to: footer rail, empty
  yard cells, drawer edges, session-tile gutters. Add as a CSS class in
  `index.css` (e.g. `.tex-yardgrid`) using existing hairline vars so it adapts
  per theme. Subtle (low opacity), never dominant.
- **Display type pairing:** introduce a distinct heading/display font for
  surface titles + the logo wordmark + glance strip, paired with the existing
  mono for data. Fonts are already imported in `index.css`
  (`VT323`, `Space Mono`, `JetBrains Mono`, `Inter`). Add `--font-display`
  var; apply to header wordmark, yard zone titles, settings category labels.
  Pick during impl (Space Mono for a "terminal-yard" feel is the natural
  candidate; VT323 only if going overtly retro).
- **Two new preferences** (extend `usePreferences` + Settings → Look):
  - `texture_enabled: boolean` (default true)
  - `display_font: string` (default the chosen display font)

Acceptance: a faint consistent grid texture is visible across footer/yard/drawer
in every theme; headings use the display font; both toggleable in Settings.

## 9. New / changed files summary

New:

- `web/src/lib/sessionState.ts` — `sessionSignal()` + `SessionState`.
- `web/src/lib/sessionState.test.ts` — branch coverage.
- `web/src/components/SettingsDrawer.tsx` — drawer shell (wraps existing
  Settings content).
- `web/src/components/HeaderOverflow.tsx` — collapsed secondary actions.
- (optional) `web/src/lib/signal.ts` — the state→treatment map if not put in
  `theme.ts`.

Changed:

- `TopBar.tsx` — glance strip, dominant-alert, overflow, ambient connection.
- `StatusBar.tsx` — three-zone ambient rail, micro system pulse, texture.
- `Overview.tsx` — yard tiles, signal-driven prominence + sort, drop stat-cards
  - pending-alerts section, demote system stats.
- `Settings.tsx` — regroup to 4 buckets, sentence case, drawer-hosted, 2 new
  prefs.
- `index.css` — `.tex-yardgrid`, `--font-display`.
- `theme.ts` — `sessionSignal` treatment map (no palette change).
- `hooks/usePreferences.ts` — `texture_enabled`, `display_font` defaults.
- `App.tsx` — compute glance counts; settings-as-drawer; pass signals down.

## 10. Rollout order & risk

Recommended sequence (each independently shippable):

1. **Phase 0** (sessionState) — no UI risk, unlocks everything.
2. **Phase 1 + 2** (header + footer) — fast, sets the calm tone app-wide, low
   risk, immediately stops looking like Guppi.
3. **Phase 3** (yard) — biggest visual divergence; do after the tone is set.
4. **Phase 4** (settings drawer) — self-contained.
5. **Phase 5** (texture/type) — polish pass over the finished surfaces.

Risks / watch-outs:

- Format-on-save reflows HTML/CSS — re-read before editing, or rewrite whole
  files (known gotcha).
- After any `web/` change run `/usr/bin/make frontend` so embedded `dist/`
  assets aren't stale; `make` is zsh-aliased — use `/usr/bin/make`.
- Keep `/settings` URL behavior working when converting to a drawer.
- Don't introduce the brand lime palette here — stay on existing vars so this is
  decoupled from a future theme pass.

## 11. Acceptance (whole effort)

- All-idle state: the app is calm — no pulsing, monochrome, near-empty header,
  ambient footer.
- A single `waiting/stuck/error` event produces exactly one consistent loud
  signal that appears identically in header, footer count, and a lit yard tile,
  and clicking any of them jumps to the session/pane.
- No feature regressions: new-session, split, port-forwards, schedules, peers,
  push, shortcuts, themes all still reachable.
- The app no longer resembles the Guppi telemetry dashboard structurally, with
  zero palette/theme reskin required.

---

## REMOVED (explicit)

These are intentionally deleted/retired by this plan:

1. **Overview stat-cards** — the 6-card row (Hosts, Sessions, Windows, Panes,
   Agents, Waiting). Counts move to header glance strip + footer. (`StatCard`
   component deletable once unused.)
2. **Overview "Pending Alerts" section** — redundant with lit yard tiles +
   header. Removed as a standalone block (logic folds into `sessionSignal`).
3. **Header always-on icon cluster** — port-forwards, schedules, new-session,
   split-pane no longer live directly on the bar; collapsed into one overflow /
   command surface.
4. **Header `NO ALERTS` text** — absence is the signal; nothing rendered when
   calm.
5. **Footer `LABEL: value` readout dump** — removed spans: `WIN`, `PANES`,
   `SESSION` (redundant with view), and the always-on numeric `CPU: x%` /
   `MEM: x%` text (replaced by an ambient micro-pulse with on-hover numbers).
   `PEERS`/`HOSTS` demoted to dots, shown only with >1 host.
6. **Multi-color "everything is loud" status painting** — the 5-status
   `statusConfig` colors are no longer painted across all surfaces; only the
   single `needs_you` signal is loud. `statusConfig` is retained ONLY for alert
   detail labels.
7. **Full-page Settings route layout** — the scroll-page + floating nav-pills
   presentation is replaced by a drawer + two-pane rail. (Section _content_ is
   kept; the 9 sections are regrouped under 4 labels.)
8. **ALL-CAPS / `tracking-widest` label styling** as the default idiom across
   header, footer, settings headers, and tiles — replaced by calm sentence-case
   - the display font.
9. **System/Process stats on the main Overview surface** — `HostStatsSection`,
   `SystemStatsCard`, `ProcessBar`, `UsageBar` are moved off the yard into a
   collapsed health strip (or drawer); not deleted, relocated.

Nothing in this plan removes a _capability_ — only surfaces, redundant
indicators, and the loud-everywhere visual idiom. Every action and datum remains
reachable.
