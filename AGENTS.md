# Project Context

This is a mixed Go + web project using chi for the HTTP router.

High-impact files (most imported, changes here affect many other files):

- path/filepath (imported by 17 files)
- encoding/json (imported by 15 files)
- crypto/rand (imported by 9 files)
- web/src/hooks/usePreferences.ts (imported by 9 files)
- web/src/theme.ts (imported by 9 files)
- net/http (imported by 8 files)
- crypto/x509 (imported by 8 files)
- os/exec (imported by 7 files)

Required environment variables (no defaults):

- PATH (pkg/commands/install/install.go)
- SHELL (pkg/tmux/client.go)
- TMPDIR (pkg/socket/socket.go)
- TMUX_PANE (pkg/commands/notify/notify.go)
- XDG_DATA_HOME (pkg/webpush/vapid.go)
- XDG_RUNTIME_DIR (pkg/socket/socket.go)

Read actual source files before implementing.
