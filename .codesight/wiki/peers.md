# Peers

> **Navigation aid.** Route list and file locations extracted via AST. Read the source files listed below before implementing or modifying this subsystem.

The Peers subsystem handles **11 routes** and touches: auth, db, queue, ai.

## Routes

- `POST` `/api/peers/bootstrap` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/api/peers` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/api/peers` [auth, db, queue, ai]
  `pkg/server/server.go`
- `PATCH` `/api/peers/{fp}` params(fp) [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/api/peers/{fp}/reconnect` params(fp) [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/peers/bootstrap` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/peers` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/peers` [auth, db, queue, ai]
  `pkg/server/server.go`
- `PATCH` `/peers/{fp}` params(fp) [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/peers/{fp}/reconnect` params(fp) [auth, db, queue, ai]
  `pkg/server/server.go`
- `DELETE` `/peers/{fp}` params(fp) [auth, db, queue, ai]
  `pkg/server/server.go`

## Source Files

Read these before implementing or modifying this subsystem:
- `pkg/server/server.go`

---
_Back to [overview.md](./overview.md)_