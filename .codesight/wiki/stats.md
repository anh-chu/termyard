# Stats

> **Navigation aid.** Route list and file locations extracted via AST. Read the source files listed below before implementing or modifying this subsystem.

The Stats subsystem handles **2 routes** and touches: auth, db, queue, ai.

## Routes

- `GET` `/api/stats` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/stats` [auth, db, queue, ai]
  `pkg/server/server.go`

## Source Files

Read these before implementing or modifying this subsystem:
- `pkg/server/server.go`

---
_Back to [overview.md](./overview.md)_