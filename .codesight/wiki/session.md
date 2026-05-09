# Session

> **Navigation aid.** Route list and file locations extracted via AST. Read the source files listed below before implementing or modifying this subsystem.

The Session subsystem handles **10 routes** and touches: auth, db, queue, ai.

## Routes

- `POST` `/api/session/new` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/api/session/rename` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/api/session/select-window` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/api/session/kill` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/api/session` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/session/new` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/session/rename` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/session/select-window` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/session/kill` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `session` [auth, db, queue, ai]
  `pkg/server/server.go`

## Source Files

Read these before implementing or modifying this subsystem:
- `pkg/server/server.go`

---
_Back to [overview.md](./overview.md)_