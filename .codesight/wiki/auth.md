# Auth

> **Navigation aid.** Route list and file locations extracted via AST. Read the source files listed below before implementing or modifying this subsystem.

The Auth subsystem handles **10 routes** and touches: auth, db, queue, ai.

## Routes

- `GET` `/api/auth/status` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/api/auth/setup` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/api/auth/login` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/api/auth/logout` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/api/auth/check` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/auth/status` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/auth/setup` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/auth/login` [auth, db, queue, ai]
  `pkg/server/server.go`
- `POST` `/auth/logout` [auth, db, queue, ai]
  `pkg/server/server.go`
- `GET` `/auth/check` [auth, db, queue, ai]
  `pkg/server/server.go`

## Middleware

- **auth** (auth) — `pkg/auth/auth.go`

## Source Files

Read these before implementing or modifying this subsystem:
- `pkg/server/server.go`

---
_Back to [overview.md](./overview.md)_