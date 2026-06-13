# Agent Setup

guppi tracks AI coding agents running inside your tmux sessions. Each agent needs a hook configured so it can notify guppi of status changes (active, waiting for input, completed, error).

## Session labels

Guppi keeps two session fields:

- **Task** — first user prompt for the session. Stays fixed for the whole session.
- **Message** — last agent message. Updates as the agent works, and falls back to task until the agent speaks.

Sidebar shows them as `task — message`. If task not set yet, sidebar shows just message.

Activity labels like `reading files`, `editing files`, `running commands`, and `searching` come from tool names, so you get a quick at-a-glance sense of what the agent is doing.

The easiest way to configure all detected agents at once:

```bash
guppi agent-setup
```

Use `--dry-run` to preview changes without writing files:

```bash
guppi agent-setup --dry-run
```

## Resilience

By default, `guppi agent-setup` appends `|| true` to all hook commands so that failures (guppi binary missing, server down, etc.) never block the agent. This means hooks are fire-and-forget — if guppi isn't reachable, the agent continues working normally.

Additionally, `guppi notify` uses 1-second timeouts for both unix socket and HTTP connections, so even without `|| true` the worst-case delay is ~2 seconds.

To disable this and let hook failures propagate to the agent:

```bash
guppi agent-setup --block
```

**When to use `--block`:** If you want the agent to be aware that guppi notifications are failing (e.g., for debugging). In normal use, the default non-blocking behavior is recommended.

**Note:** OpenCode hooks are always resilient regardless of `--block`, since the plugin uses native try/catch error handling.

## Supported Agents

### Claude Code

**Auto-configured by `guppi agent-setup`.**

guppi adds hooks to `~/.claude/settings.json` that fire on tool use, notifications (permission prompts, input dialogs), and task completion. The Stop hook reads Claude's transcript file to capture the last assistant message, so Claude sessions show a meaningful last message instead of only "Task complete".

**Manual setup:** Add to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "guppi notify -t claude -s active -m 'Session started' || true"
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "guppi notify -t claude -s active -m 'Thinking' --stdin || true"
          }
        ]
      }
    ],
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "guppi notify -t claude --stdin || true"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "guppi notify -t claude --stdin || true"
          }
        ]
      }
    ],
    "Notification": [
      {
        "matcher": "permission_prompt",
        "hooks": [
          {
            "type": "command",
            "command": "guppi notify -t claude -s waiting -m 'Permission needed' || true"
          }
        ]
      },
      {
        "matcher": "elicitation_dialog",
        "hooks": [
          {
            "type": "command",
            "command": "guppi notify -t claude -s waiting -m 'Needs input' || true"
          }
        ]
      }
    ],
    "Stop": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "guppi notify -t claude -s completed --stdin || true"
          }
        ]
      }
    ]
  }
}
```

### Codex

**Auto-configured by `guppi agent-setup`.**

Codex supports a `notify` key in `~/.codex/config.toml`. This fires when the agent needs user attention and passes the last assistant message as a JSON blob in `$1`.

**Manual setup:** Add to `~/.codex/config.toml`. The `notify` line **must appear before any table headers** (like `[sandbox]`) or Codex's TOML parser will misinterpret it:

```toml
model = "o4-mini"
notify = ["bash", "-c", "guppi notify -t codex --event-data \"$1\" || true", "--"] # guppi-agent-hook

[sandbox]
# ... rest of config
```

**How it works:**

Codex passes a JSON blob as an additional argument when the agent completes a turn and needs user input. The `--event-data` flag tells guppi to parse this JSON natively. Fields extracted:

- `type` — event type (currently `agent-turn-complete`)
- `last-assistant-message` — truncated to 200 chars and used as the notification message
- `thread-id`, `turn-id`, `cwd` — available for context

The `agent-turn-complete` event maps to guppi's `waiting` status, which triggers an alert in the UI and a push notification.

No external dependencies required (no bash, no jq).

### OpenCode

**Auto-configured by `guppi agent-setup`.**

guppi writes a JavaScript plugin to `~/.config/opencode/plugins/guppi.js` that hooks into OpenCode's event system. The plugin registers handlers for `permission.ask`, `permission.asked`, `permission.replied`, `command.execute.before`, `tool.execute.before/after`, `session.idle`, and `session.error`.

**Manual setup:** Create `~/.config/opencode/plugins/guppi.js` with a Bun-compatible plugin module that calls `guppi notify` for each event type:

| OpenCode Event           | guppi Status | Message             |
| ------------------------ | ------------ | ------------------- |
| `permission.ask`         | `waiting`    | "Permission needed" |
| `permission.asked`       | `waiting`    | "Permission needed" |
| `permission.replied`     | `active`     | "Working"           |
| `command.execute.before` | `active`     | "Running command"   |
| `tool.execute.before`    | `active`     | "Using tool"        |
| `tool.execute.after`     | `active`     | "Working"           |
| `session.idle`           | `completed`  | "Idle"              |
| `session.error`          | `error`      | "Error"             |

**Note:** `permission.ask` fires before the dialog appears (earlier than `permission.asked`); both are registered for redundancy.

Run `guppi agent-setup` to generate the plugin file automatically.

### Pi

**Auto-configured by `guppi agent-setup`.**

guppi writes a TypeScript extension to `~/.pi/agent/extensions/guppi.ts`.

| Pi Event                   | guppi Status | Message                               |
| -------------------------- | ------------ | ------------------------------------- |
| `session_start`            | `active`     | "Session started" / "Session resumed" |
| `before_agent_start`       | `active`     | "Thinking"                            |
| `agent_start`              | `active`     | "Working" (with first user prompt)    |
| `tool_execution_start`     | `active`     | _(activity label from tool name)_     |
| `tool_execution_end`       | `active`     | "Working"                             |
| `tool_call` (confirmation) | `waiting`    | "Permission needed"                   |
| `tool_result` (isError)    | `error`      | "Tool error"                          |
| `agent_end`                | `completed`  | _(last agent message)_                |
| `session_shutdown`         | `completed`  | "Session ended"                       |

## Status message support matrix

Quick reference for which status types each agent supports via hooks:

| Agent           | Session start      | Working / tool use                                       | Permission / waiting                     | Error              | Task complete               |
| --------------- | ------------------ | -------------------------------------------------------- | ---------------------------------------- | ------------------ | --------------------------- |
| **Claude Code** | ✅ `SessionStart`  | ✅ `PreToolUse` / `PostToolUse`                          | ✅ `Notification`                        | ❌ no error hook   | ✅ `Stop`                   |
| **OpenCode**    | ❌ no hook         | ✅ `tool.execute.before/after`, `command.execute.before` | ✅ `permission.ask` / `permission.asked` | ✅ `session.error` | ✅ `session.idle`           |
| **Codex**       | ❌ no hook         | ❌ no per-tool hook                                      | ❌ no hook                               | ❌ no hook         | ✅ `notify` (turn complete) |
| **Pi**          | ✅ `session_start` | ✅ `tool_execution_start/end`                            | ✅ `tool_call`                           | ✅ `tool_result`   | ✅ `agent_end`              |

**Gaps:**

- **Claude Code** has no error hook; errors surface via inactivity detection or agent output.
- **OpenCode** has no session-start hook; first tool-use event is the earliest signal.
- **Codex** only has one hook (`notify`) that fires on turn-complete. All other states (working, waiting, error) rely on inactivity detection.

## The `notify` command

Under the hood, all agent hooks call `guppi notify`. You can also use it directly:

```bash
# Basic usage
guppi notify -t claude -s waiting -m "Needs approval"
guppi notify -t codex -s active
guppi notify -t claude -s completed

# Read event JSON from stdin (used by some agent hooks)
echo '{"hook_event_name":"Stop","last_assistant_message":"Done"}' | guppi notify -t codex --stdin
```

**Flags:**

| Flag        | Alias | Description                                         |
| ----------- | ----- | --------------------------------------------------- |
| `--tool`    | `-t`  | Agent name: `claude`, `codex`, `opencode`, `pi`     |
| `--status`  | `-s`  | Status: `active`, `waiting`, `completed`, `error`   |
| `--message` | `-m`  | Human-readable message                              |
| `--stdin`   |       | Read hook event JSON from stdin                     |
| `--session` |       | tmux session name (auto-detected)                   |
| `--window`  |       | tmux window index (auto-detected)                   |
| `--pane`    |       | tmux pane ID (auto-detected)                        |
| `--server`  |       | guppi server URL (default: `http://localhost:7654`) |
| `--socket`  |       | Unix socket path (auto-detected)                    |

**Communication:** `guppi notify` tries the Unix socket first (zero-config when guppi server is running locally), then falls back to HTTP. Both use 1-second timeouts to minimize impact on agent performance.

## Inactivity-based waiting detection

Claude Code sends explicit "waiting" events when it needs input (permission prompts, input dialogs). Other tools (Codex, OpenCode) don't have equivalent hooks.

To bridge this gap, guppi includes an **inactivity promoter**: if a non-Claude tool sends "active" events but then goes quiet for 30 seconds, guppi automatically generates a synthetic "waiting" event with the message "May need attention". This surfaces the alert in the UI and triggers push notifications, just like a native waiting event would.

This only applies to tools without native waiting support — Claude's explicit hooks are always trusted and never overridden.

The timeout is 30 seconds by default. This balances catching idle agents quickly against false positives during normal pauses between tool calls.

## Status values

| Status      | Meaning                   | UI behavior                      |
| ----------- | ------------------------- | -------------------------------- |
| `active`    | Agent is working normally | Badge in sidebar                 |
| `waiting`   | Agent needs user input    | Alert banner + push notification |
| `error`     | Agent hit an error        | Alert banner + push notification |
| `completed` | Agent finished its task   | Clears alerts                    |
