# Project Context

This is a mixed project using chi.

The API has 70 routes. See .codesight/routes.md for the full route map with methods, paths, and tags.
The UI has 13 components. See .codesight/components.md for the full list with props.
Middleware includes: auth.

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
- SHELL (pkg/model/client.go)
- TMPDIR (pkg/socket/socket.go)
- TMUX_PANE (pkg/commands/notify/notify.go)
- XDG_DATA_HOME (pkg/webpush/vapid.go)
- XDG_RUNTIME_DIR (pkg/socket/socket.go)

Read .codesight/wiki/index.md for orientation (WHERE things live). Then read actual source files before implementing. Wiki articles are navigation aids, not implementation guides.
Read .codesight/CODESIGHT.md for the complete AI context map including all routes, schema, components, libraries, config, middleware, and dependency graph.
