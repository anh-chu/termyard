# Push

> **Navigation aid.** Route list and file locations extracted via AST. Read the source files listed below before implementing or modifying this subsystem.

The Push subsystem handles **4 routes** and touches: auth, db, queue, ai.

## Routes

- `GET` `/api/push/vapid-key` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/api/push/unsubscribe` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/push/vapid-key` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/push/unsubscribe` [auth, db, queue, ai]
  `pkg/server/server.go`

## Source Files

Read these before implementing or modifying this subsystem:
- `pkg/server/server.go`

---
_Back to [overview.md](./overview.md)_