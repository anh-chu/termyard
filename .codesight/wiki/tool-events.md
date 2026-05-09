# Tool-events

> **Navigation aid.** Route list and file locations extracted via AST. Read the source files listed below before implementing or modifying this subsystem.

The Tool-events subsystem handles **4 routes** and touches: auth, db, queue, ai.

## Routes

- `GET` `/api/tool-events` [auth, db, queue, ai]
  `pkg/server/server.go`
- `DELETE` `/api/tool-events` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/tool-events` [auth, db, queue, ai]
  `pkg/server/server.go`
- `DELETE` `/tool-events` [auth, db, queue, ai]
  `pkg/server/server.go`

## Source Files

Read these before implementing or modifying this subsystem:
- `pkg/server/server.go`

---
_Back to [overview.md](./overview.md)_