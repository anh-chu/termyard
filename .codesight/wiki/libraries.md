# Libraries

> **Navigation aid.** Library inventory extracted via AST. Read the source files listed here before modifying exported functions.

**61 library files** across 2 modules

## Pkg (45 files)

- `pkg/peer/protocol.go` — NewMessage, Message, AuthPayload, ChallengePayload, StateUpdatePayload, StateEventPayload, …
- `pkg/auth/auth.go` — NewPasswordStore, NewSessionManager, Middleware, SetupHandler, LoginHandler, LogoutHandler, …
- `pkg/preferences/preferences.go` — Default, NewStore, Terminal, Sidebar, Notifications, AgentBanner, …
- `pkg/tmux/controlmode.go` — ControlSessionName, WithRefreshDelay, WithOnConnect, WithOnDisconnect, WithOnOutput, NewControlMode, …
- `pkg/tmux/sessionmeta.go` — NormalizeAgentType, IsShellCommand, PrimaryPane, InferAgentType, ResolveProjectPath, ExtractPromptPreview
- `pkg/identity/identity.go` — Generate, Verify, LoadOrCreate, Load, Identity
- `pkg/peer/bootstrap.go` — NormalizeAddress, SendBootstrap, BootstrapRequest, BootstrapResponse, BootstrapError
- `pkg/peer/manager.go` — NewPeerConnection, NewManager, HostState, PeerConnection, Manager
- `pkg/toolevents/tracker.go` — NewTracker, Event, PaneKey, SessionMeta, Tracker
- `pkg/activity/tracker.go` — NewTracker, SessionActivity, Snapshot, Tracker
- `pkg/git/worktree.go` — IsWorktree, FindMainWorktreeRoot, RemoveWorktree, CreateWorktree
- `pkg/peer/session.go` — SessionDeps, LayoutSink, BrowserBroadcaster, LayoutSource
- `pkg/state/manager.go` — NewManager, SessionMetadata, Manager, StateEvent
- `pkg/tmux/types.go` — Session, Window, PaneDetailed, Pane
- `pkg/toolevents/reconciler.go` — NewReconciler, PaneState, PaneInfo, Reconciler
- `pkg/ws/hub.go` — CheckSameOrigin, NewHub, Hub, ActivitySource
- `pkg/agentcheck/agentcheck.go` — CheckAgents, AgentStatus, StatusResult
- `pkg/identity/peers.go` — NewPeerStore, Peer, PeerStore
- `pkg/layout/layout.go` — NewStore, Layout, Store
- `pkg/peer/pty_manager.go` — NewPTYManager, PTYManager, ActivePTY
- `pkg/peer/pty_relay.go` — NewPTYRelay, GenerateStreamID, PTYRelay
- `pkg/peer/supervisor.go` — NewLinkSupervisor, LinkSnapshot, LinkSupervisor
- `pkg/portforward/store.go` — NewStore, Forward, Store
- `pkg/socket/socket.go` — DefaultPath, EnsureDir, Cleanup
- `pkg/stats/stats.go` — SystemStats, ProcessCountsFromSessions, ProcessEntry
- _…and 20 more files_

## Web (16 files)

- `web/src/lib/paneTree.ts` — getLeaves, findLeaf, splitLeaf, removeLeaf, replaceLeaf, updateRatio, …
- `web/src/hooks/useSessions.ts` — sessionKey, parseSessionKey, useSessions, Pane, Window, Session
- `web/src/theme.ts` — applyTheme, getXtermTheme, ThemePreset, toolColors, statusConfig, themePresets
- `web/src/hooks/usePreferences.ts` — usePreferencesProvider, usePreferences, Preferences, defaultPreferences, PreferencesContext
- `web/src/hooks/usePortForwards.ts` — usePortForwards, PortForward, ForwardMode
- `web/src/hooks/useActivity.ts` — useActivity, ActivitySnapshot
- `web/src/hooks/useHosts.ts` — useHosts, Host
- `web/src/hooks/useLayoutSync.ts` — useLayoutSync, LAYOUT_CLIENT_ID
- `web/src/hooks/useToolEvents.ts` — useToolEvents, ToolEvent
- `web/src/hooks/useAuth.ts` — useAuth
- `web/src/hooks/useNotifications.ts` — useNotifications
- `web/src/hooks/usePushNotifications.ts` — usePushNotifications
- `web/src/hooks/useTerminal.ts` — useTerminal
- `web/src/hooks/useWebSocket.ts` — useWebSocket
- `web/src/lib/hostColor.ts` — hostColor
- `web/src/lib/utils.ts` — cn

---
_Back to [overview.md](./overview.md)_