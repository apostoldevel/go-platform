# go-platform

Cross-project Go platform for Apostol-based backends: a thin, stateless adapter between an HTTP
gateway and the PostgreSQL `api.*` layer of [db-platform](https://github.com/apostoldevel/db-platform).

Planned packages (contract first, code after it is signed):

- `gatewayclient` — connects to the gateway over WebSocket, registers its route prefixes, keeps
  the heartbeat, announces `draining` on `SIGTERM`, reconnects with backoff.
- `request` — one HTTP request = one database transaction: `api.authorize` → `api.*` →
  `api.log_request` → `COMMIT`; on failure `ROLLBACK`, then the error is explained by the
  database catalogue and returned as `application/problem+json` (RFC 9457).
- `query` — translation of list parameters (`search`, `filter`, `orderby`, paging) to the jsonb
  arguments of `api.sql()`.
- `openapi` — OpenAPI 3.1 from the module's types.

Module path: `github.com/apostoldevel/go-platform`. Consumed by a project as a git submodule
(`go/platform`), the way `db-platform` is consumed as `db/sql/platform`.
