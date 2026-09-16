# go-platform

Cross-project Go platform for Apostol-based backends: a thin, stateless adapter between an HTTP
gateway and the PostgreSQL `api.*` layer of [db-platform](https://github.com/apostoldevel/db-platform).

Layout — the Go tree mirrors the SQL tree of db-platform, one package per SQL module or entity in
the folder of its SQL path; the shared library is under `lib/`:

- `module.go` — `Module { Name; Prefixes; Routes(mux) }`, the contract every GoAPI package
  implements, and `New(cfg, modules…)` — the host that composes them into the one handler a
  process serves: JWT verified before any route, `X-Request-Id`, in-flight accounting,
  404/405 as `application/problem+json`. One binary per project: the imports of its `main` are
  the build's manifest, the way `create.psql` is for SQL.
- `version.go` — `DBPlatform`, the db-platform version the integration tests last ran against;
  the run checks it (`GO_TEST_DB_PLATFORM_VERSION`).
- `lib/gatewayclient` — connects to the gateway over WebSocket, registers the route prefixes,
  keeps the heartbeat, announces `draining` on `SIGTERM`, reconnects with backoff.
- `lib/pgtx` — one HTTP request = one database transaction: `api.authorize` → `api.*` →
  `api.log_request` → `COMMIT`; on failure `ROLLBACK`, then the error is explained by the
  database catalogue and returned as `problem+json` (RFC 9457).
- `lib/query` — translation of list parameters (`filter`, `sort`, `fields`, paging) to the
  jsonb arguments of `api.sql()`.
- `lib/problem`, `lib/auth/jwt`, `lib/gateway/frame` — problem+json, HS256/384/512 JWT
  verification, the control-plane frame.
- `cmd/gatewaystub`, `internal/gatewaystub` — a stub of the gateway's control plane for tests
  and local runs.

Module path: `github.com/apostoldevel/go-platform`. Consumed by a project as a git submodule
(`go/platform`), the way `db-platform` is consumed as `db/sql/platform`.

License: MIT.
