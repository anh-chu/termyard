# Tls

> **Navigation aid.** Route list and file locations extracted via AST. Read the source files listed below before implementing or modifying this subsystem.

The Tls subsystem handles **6 routes** and touches: auth, db, queue, ai.

## Routes

- `GET` `/api/tls/status` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/api/tls/ca.crt` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/api/tls/ca.mobileconfig` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/tls/status` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/tls/ca.crt` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/tls/ca.mobileconfig` [auth, db, queue, ai]
  `pkg/server/server.go`

## Source Files

Read these before implementing or modifying this subsystem:
- `pkg/server/server.go`

---
_Back to [overview.md](./overview.md)_