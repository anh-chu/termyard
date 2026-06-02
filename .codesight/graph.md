# Dependency Graph

## Most Imported Files (change these carefully)

- `path/filepath` — imported by **22** files
- `encoding/json` — imported by **22** files
- `net/http` — imported by **11** files
- `os/exec` — imported by **10** files
- `web/src/lib/utils.ts` — imported by **10** files
- `web/src/hooks/usePreferences.ts` — imported by **9** files
- `web/src/theme.ts` — imported by **9** files
- `encoding/base64` — imported by **8** files
- `web/src/hooks/useSessions.ts` — imported by **7** files
- `web/src/hooks/useToolEvents.ts` — imported by **6** files
- `crypto/rand` — imported by **5** files
- `web/src/hooks/useHosts.ts` — imported by **5** files
- `encoding/hex` — imported by **4** files
- `net/url` — imported by **4** files
- `web/src/hooks/useActivity.ts` — imported by **3** files
- `web/src/hooks/usePushNotifications.ts` — imported by **3** files
- `compress/gzip` — imported by **2** files
- `net/http/httptest` — imported by **2** files
- `web/src/components/Terminal.tsx` — imported by **2** files
- `web/src/lib/paneTree.ts` — imported by **2** files

## Import Map (who imports what)

- `path/filepath` ← `pkg/agentcheck/agentcheck.go`, `pkg/auth/auth.go`, `pkg/commands/agent-setup/agent_setup.go`, `pkg/commands/install/install.go`, `pkg/commands/update/update.go` +17 more
- `encoding/json` ← `pkg/auth/auth.go`, `pkg/commands/agent-setup/agent_setup.go`, `pkg/commands/notify/notify.go`, `pkg/commands/update/update.go`, `pkg/identity/identity.go` +17 more
- `net/http` ← `pkg/auth/auth.go`, `pkg/commands/notify/notify.go`, `pkg/commands/update/update.go`, `pkg/peer/bootstrap.go`, `pkg/peer/bootstrap_test.go` +6 more
- `os/exec` ← `pkg/agentcheck/agentcheck.go`, `pkg/commands/agent-setup/agent_setup.go`, `pkg/commands/install/install.go`, `pkg/commands/notify/notify.go`, `pkg/commands/update/update.go` +5 more
- `web/src/lib/utils.ts` ← `web/src/components/AgentMark.tsx`, `web/src/components/NewSessionModal.tsx`, `web/src/components/PortForwardModal.tsx`, `web/src/components/QuickSwitcher.tsx`, `web/src/components/Settings.tsx` +5 more
- `web/src/hooks/usePreferences.ts` ← `web/src/App.tsx`, `web/src/components/NewSessionModal.tsx`, `web/src/components/Overview.tsx`, `web/src/components/Settings.tsx`, `web/src/components/Setup.tsx` +4 more
- `web/src/theme.ts` ← `web/src/App.tsx`, `web/src/components/AgentMark.tsx`, `web/src/components/Overview.tsx`, `web/src/components/QuickSwitcher.tsx`, `web/src/components/Settings.tsx` +4 more
- `encoding/base64` ← `pkg/identity/identity.go`, `pkg/peer/handler.go`, `pkg/peer/pty_manager.go`, `pkg/peer/pty_relay.go`, `pkg/peer/session.go` +3 more
- `web/src/hooks/useSessions.ts` ← `web/src/App.tsx`, `web/src/components/NewSessionModal.tsx`, `web/src/components/Overview.tsx`, `web/src/components/QuickSwitcher.tsx`, `web/src/components/Sidebar.tsx` +2 more
- `web/src/hooks/useToolEvents.ts` ← `web/src/App.tsx`, `web/src/components/Overview.tsx`, `web/src/components/QuickSwitcher.tsx`, `web/src/components/Sidebar.tsx`, `web/src/components/TopBar.tsx` +1 more
