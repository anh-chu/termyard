Task statement:
Implement session-list and session-creation features in guppi inspired by the similar repo represented in repomix-output.xml.

Desired outcome:
- Session list shows agent identity with recognizable branding/icons.
- Session list can show a preview of the latest prompt/message for a session.
- Session list supports filtering by multiple projects.
- New session flow can create a session for a chosen agent in a chosen location.
- Session list can show the agent CLI session id when available.
- Session list supports drag-and-drop reordering.
- Session list shows an uptime badge.

Known facts/evidence:
- Current frontend session model only includes tmux-native metadata plus windows/panes.
- Current sidebar alphabetically sorts sessions and has no project filter or reorder support.
- Current new-session flow only accepts session name and optional host.
- Current tmux backend exposes session id/name/created/attached/last_activity and pane current command/pid.
- Current notify path already parses Codex event fields including thread-id, cwd, and last-assistant-message, but does not persist them.
- toolColors already include claude/codex/copilot/opencode.
- repomix-output.xml contains concrete implementations for agent identity, last-user-message preview, project filter dropdown, agent-aware new-session modal, and drag-and-drop ordering.

Constraints:
- No new dependencies without explicit request.
- Must preserve existing behavior where possible.
- Need verification evidence before completion.
- Must not revert unrelated dirty-worktree changes.

Unknowns/open questions:
- Whether drag-and-drop can be implemented dependency-free with acceptable UX.
- Whether latest prompt preview should use existing tool-event/message data, pane capture, or new persisted metadata.
- Whether session id display should cover Codex only initially or additional agents where available.
- Exact project-path derivation source for sessions across local and remote hosts.

Likely codebase touchpoints:
- web/src/components/Sidebar.tsx
- web/src/components/NewSessionModal.tsx
- web/src/hooks/useSessions.ts
- web/src/App.tsx
- web/src/hooks/usePreferences.ts
- pkg/tmux/types.go
- pkg/tmux/client.go
- pkg/server/server.go
- pkg/peer/protocol.go
- pkg/peer/client.go
- pkg/commands/notify/notify.go
- state/session tracking modules if session metadata persistence is needed
