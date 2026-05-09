# Pair

> **Navigation aid.** Route list and file locations extracted via AST. Read the source files listed below before implementing or modifying this subsystem.

The Pair subsystem handles **4 routes** and touches: auth, db, queue, ai.

## Routes

- `POST` `http://localhost/api/pair`
  `pkg/commands/pair/pair.go`
- `POST` `/api/pair` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/pair` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/api/pair/complete` [auth, db, queue, ai]
  `pkg/server/server.go`

## Source Files

Read these before implementing or modifying this subsystem:
- `pkg/commands/pair/pair.go`
- `pkg/server/server.go`

---
_Back to [overview.md](./overview.md)_