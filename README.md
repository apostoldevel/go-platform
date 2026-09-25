[![ru](https://img.shields.io/badge/lang-ru-green.svg)](README.ru-RU.md)

Go Platform
-

**Go layer** for **Apostol CRM**[^crm].

Description
-

**Go Platform** is a library and a set of ready-made packages for writing **out-of-process API modules** in Go for the [Apostol (C++20)](https://github.com/apostoldevel/libapostol) framework. A module built on it is an HTTP server that answers `/api/v2/*`, registers itself with [Gateway API](https://github.com/apostoldevel/module-GatewayAPI) over WebSocket and calls the `api.*` functions of [db-platform](https://github.com/apostoldevel/db-platform) in PostgreSQL. The module is thin and stateless: authorisation, access rules, workflow and the business logic stay in the database, exactly where the in-process [AppServer](https://github.com/apostoldevel/module-AppServer) leaves them for `/api/v1/*`.

The repository holds two things:

* **The library** (`lib/`, root package `platform`) — the module contract and the host that composes packages into one handler; the gateway client; one request = one database transaction; the translation of list parameters to `api.sql()`; `application/problem+json` (RFC 9457); local JWT verification.
* **The platform packages** — one Go package per SQL module of db-platform (`admin`, `workflow`, `registry`, `log`, …), each exposing that module's `api.*` functions as `/api/v2` resources. A project adds its own packages beside them, one per entity of its configuration.

Key characteristics:

* **The Go tree mirrors the SQL tree.** A package lives in the folder of its SQL module or entity (`workflow/`, `entity/object/`, `entity/object/document/<x>/`) and is named after it. The shared code is under `lib/` (`go` is a keyword and cannot end an import path).
* **One binary per project.** The imports of its `main` are the build's manifest, the way `create.psql` is for SQL: what is not imported is not in the process. One `platform.New`, one socket to the gateway, one connection pool.
* **The database is trusted with everything but the signature.** The JWT is verified in Go before any route — the database trusts the session code it is given — and then every `api.*` call of the request runs under `api.authorize` inside one transaction.
* **Every answer of the process is JSON**: a row, a list `{items, total, limit, offset}` or a `problem+json` — including `404`, `405` (with `Allow`) and a query string the host cannot parse (`400`). Nothing of `net/http`'s own text answers reaches the client.
* **Versions are linked by a constant.** `platform.DBPlatform` is the db-platform version the integration tests last ran against; the run checks it against the database's `VERSION` file.

### How it fits into Apostol

```
client ── /api/v1/*  ──► worker: AppServer   ──► PostgreSQL (api.*)
       ── /oauth2/*  ──► worker: AuthServer                                    ┌─ this repository ─┐
       ── /api/v2/*  ──► worker: GatewayAPI ── HTTP ──► module at ipv4:port ──►│ platform.New       │──► PostgreSQL (api.*)
       ── /gateway/* ──► worker: GatewayAPI ◄── WebSocket ── module ───────────│ lib/gatewayclient  │
                                                                               └────────────────────┘
```

Requests travel **gateway → module** over HTTP; the control channel travels **module → gateway** over WebSocket. A module needs nothing of the gateway's configuration: it connects, says which prefixes it serves and at which address it listens, and is in rotation the moment its registration is accepted.

Layout
-

```
module.go              package platform: Module { Name; Prefixes; Routes(mux) }, New(cfg, modules…), Prefixes(modules…)
version.go             DBPlatform — the db-platform version the integration tests ran against
lib/
  auth/jwt/            HS256/384/512 verification with the secrets of the OAuth2 providers; Keyring, Claims
  gateway/frame/       the control-plane frame {t,u,a,p,c,m}: CALL, CALLRESULT, CALLERROR; 64 KiB limit
  gatewayclient/       connect, /register, heartbeat, /status, /unregister; /ping, /drain, /reload; reconnect
  pgtx/                one request = one transaction: api.authorize → SAVEPOINT → api.* → api.log_request → COMMIT; a refusal is journalled too
  problem/             application/problem+json with the database's error catalogue
  query/               ?filter[…]&sort=&fields=&page[limit]= → the search/orderby/fields jsonb of api.sql()
  rest/                the shape of a resource: list, row + ETag, create/update/delete, Idempotency-Key, If-Match
admin/ api/ current/ error/ kladr/ log/ notification/ observer/ registry/ resource/ verification/ workflow/
entity/object/         the platform packages, one per SQL module of db-platform (see the table below)
cmd/gatewaystub/       a stand-alone stub of the gateway's control plane for local runs
internal/gatewaystub/  the same stub as a test double; internal/resttest — integration-test harness
```

Module contract
-

A GoAPI package is a `platform.Module`:

```go
type Module interface {
    Name() string            // the SQL module or entity it mirrors: "workflow", "client"
    Prefixes() []string      // what goes to the gateway's /register: "/api/v2/clients"
    Routes(mux *http.ServeMux)  // "GET /api/v2/clients/{id}" … on the shared mux
}
```

`platform.New(cfg, modules…)` composes modules into the one `http.Handler` a process serves. Before any route it verifies the bearer token with `cfg.Keys`, establishes the session (`code` = JWT `sub`, agent from `User-Agent`, host decided from `X-Forwarded-For` and the peer by `cfg.TrustedProxies`), returns `X-Request-Id` unchanged, counts the request in-flight and parses the query string; two modules claiming the same prefix are refused at start-up, not at `/register`. Every 401 the host makes is `problem+json` of type `unauthorized` carrying the error-catalogue code in `code` — `ERR-401-001` no bearer, or a token that does not verify, `ERR-401-008` a verified token that has expired — titled by the catalogue message when `cfg.Catalogue` is set (a `pgtx.Runner` after `Detect` reads the titles once; the host never asks the database per refusal), `"Unauthorized"` otherwise. Every failure `Verify` can meet before the signature holds (malformed, unknown audience, algorithm, signature, issuer) is **one** answer — code and `detail` alike: the audience is checked before the signature, and telling them apart would let an unsigned token enumerate client ids; the exact reason goes to `cfg.Logger` at debug. `pgtx` answers a session the database does not know the same way, with `ERR-401-001`. Every 401 of the process — the host's and `pgtx`'s alike, whatever the code — carries `WWW-Authenticate` (RFC 6750 §3): the bare `Bearer` when the request presented no bearer at all (§3.1: no error code then), `Bearer error="invalid_token"` otherwise, with the `detail` as `error_description` when it is printable ASCII without `"` or `\` (a localized catalogue text stays in the body only). One place makes it — `problem.(*Problem).Write`; `(*Problem).NoCredentials()` marks the bare case. `platform.Prefixes(modules…)` is the union of the prefixes in registration order, with a prefix nested under another one of the same process left out — the gateway routes by longest prefix and refuses an overlap.

Inside a handler, `platform.SessionOf(r)` is the session and `rest.Doer` (a `pgtx.Runner` in production) runs the transaction:

```go
func (m *module) get(w http.ResponseWriter, r *http.Request) {
    id, err := rest.IDOf(r)                       // {id} is a UUID or 400 before the database
    if err != nil { rest.Fail(w, r, m.log, err); return }
    var row json.RawMessage
    err = m.doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil),
        func(ctx context.Context, tx pgx.Tx) error {
            return tx.QueryRow(ctx, "SELECT row_to_json(t) FROM api.get_client($1) t", id).Scan(&row)
        })
    …
}
```

Most packages do not write handlers at all: `rest.Resource` names the `api.get_<x>` / `api.list_<x>` / `api.count_<x>` functions of a resource, `rest.Writable` adds `api.set_<x>` / `api.delete_<x>` with a typed body, and `Routes` registers the verbs (five, or four when the resource has no `api.delete_<x>` — then `DELETE` is `405`).

Request path
-

One HTTP request is one database transaction (`lib/pgtx`):

```
BEGIN
  SELECT * FROM api.authorize($session, $agent, $host)   -- or api.authorize_local when the database has it
  SAVEPOINT request                                      -- when the database journals
  … the handler's api.* calls …
  SELECT api.log_request(…, status)                      -- when the database has it
COMMIT
```

A refusal is journalled as well: the handler's work is undone with `ROLLBACK TO SAVEPOINT`, the error is explained, then `api.log_request(…, 4xx)` runs under the session context set before the savepoint (it survives the partial rollback) and the transaction commits with the audit row alone — `db.api_log` shows who was refused what, the way `api.run` shows it for v1. Since db-platform 1.2.24 `api.log_request` takes a seventh parameter, `pError`: the catalogue code of the refusal (`ERR-400-001`, …) lands in the row as `_request.error`, so a 4xx line says what was refused, not only that it was; a module-side refusal (not-found, validation) has no code and writes none. Which shape the database has is read off `pg_proc.pronargs` at `Detect`, not off a flag — one binary runs before and after the patch. A session the database refuses is journalled without one — whether `api.authorize_local` answered `false` (`401`) or raised (IP table, locked user, expired password: the catalogue code's status, in a fresh transaction, since the raise aborted the request's); a token the host refuses never reaches the database. The audit row is written under a context of its own, so a client that gave up on the answer does not take the row with it. `pgtx.Request.LogID` is the row written.

Session context in db-platform is per transaction, not per connection: under a pool the next request would otherwise inherit a stranger's session, so nothing of a request runs outside its transaction. On an error the transaction is rolled back and the message is explained by the database's catalogue (`api.parse_message`) on a separate connection, in its own transaction, and returned as `problem+json`; an aborted transaction cannot run the query that explains its own error. `Runner.Detect` probes once at start which of `api.authorize_local`, `api.log_request` (and with how many parameters) and `api.parse_message` the database offers.

### Conventions of `/api/v2`

| | |
|---|---|
| Resources | plural nouns, one per `api.*` family: `/api/v2/users`, `/api/v2/users/{id}`, `/api/v2/users/{id}/groups` |
| List | `GET /api/v2/<xs>?filter[state]=enabled&filter[created][gte]=…&filter[state][in]=a,b&sort=-created,name&fields=id,name&page[limit]=50&page[offset]=100` → `{items, total, limit, offset}`; `filter` and `sort` become the `search`/`orderby` jsonb of `api.sql()` — the operator language stays in the database, Go only renames; `fields` is applied to the rows in Go |
| Row | `GET …/{id}` → the row with a weak `ETag`; `If-None-Match` → `304`; a `null` row is `404` |
| Create | `POST /api/v2/<xs>` → `201`, `Location`, `ETag`; `Idempotency-Key` replays the answer for the same body |
| Update | `PATCH …/{id}` with `If-Match` (`428` without it, `412` when stale) |
| Delete | `DELETE …/{id}` → `204` |
| Actions | `POST …/{id}/actions/<verb>` for what is not a field change (workflow methods, `copy`, `clone`, …) |
| Bodies | JSON objects, unknown keys refused (`400`), as `CheckJsonbKeys` does in the database |
| Errors | `application/problem+json`: `{type: "urn:apostol:error:ERR-400-032", title, status, detail, instance, request_id, code}`; the code and text come from the database's error catalogue; a constraint the database refuses is `400` (`409` for a duplicate key, and for a foreign key that refuses a `DELETE` — the resource is still referenced) |
| Headers | `Authorization: Bearer <access token>` in, `X-Request-Id` in and out unchanged |

### What v1 has that v2 does not

`/ping`, `/time`, `/authenticate`, `/authorize`, `/su`, `/run`, `/sign/in`, `/sign/up`, `/sign/out` and `/observer/subscribe` are not `/api/v2` resources by design: a health check is the module's own endpoint; the time is the `Date` header; signing in, out and switching users are the OAuth2 server's (`/oauth2/token`, `/oauth2/revoke`) — a module receives a ready JWT; `/authorize` is the first step of every transaction; `/run` multiplexed v1 paths; subscriptions are made over the WebSocket API, not over HTTP.

Control plane
-

`lib/gatewayclient` is the module's side of the [Gateway API](https://github.com/apostoldevel/module-GatewayAPI) control plane (its README, section *Control plane*, is the protocol):

* **Handshake** — `GET {GATEWAY_URL}/{module}/{instance}` with `Authorization: Bearer <client_credentials token>`; `Config.Token` returns the token and is called on every (re)connection, so it may refresh.
* **`/register`** — module, instance, version, build, `address` (an IPv4 literal `host:port` of the data plane — empty `Config.Address` takes the local address of the control socket plus `ListenPort`), the prefixes and the capacity. The reply carries `heartbeat_interval`.
* **`/heartbeat`** every interval with the in-flight count (`Config.InFlight`); **`/status`** on `ready` / `draining` / `overloaded` (`SetOverloaded`); **`/unregister`** before closing.
* **Commands** — `/ping` is answered with the in-flight count; `/drain` starts the drain; `/reload` calls `Config.OnReload`, and a changed address, prefixes or capacity make the client re-register.
* **Drain** (`Client.Drain`, the caller's answer to `SIGTERM`) — `/status draining` → wait for in-flight requests (`DrainDeadline`, default 30 s) → `/unregister` → close `1000`. Exit before that and the in-flight requests are `502` for the client.
* **Reconnect** — after a lost socket or a failed dial with `DefaultReconnect` (1 → 30 s, ±20 %); after a refused registration or a rejected handshake after 30 s; close `1001` (replaced by a newer registration with the same name) ends `Run` with `ErrReplaced` — the module must not reconnect; a frame above 64 KiB closes the socket with `1009`.

```go
gw, err := gatewayclient.New(gatewayclient.Config{
    URL: cfg.GatewayURL, Module: "example-api", Instance: cfg.Instance, Version: version, Build: build,
    Address: cfg.AdvertiseAddr, Prefixes: platform.Prefixes(mods...), Capacity: cfg.Capacity,
    Token:    token,                                   // client_credentials from /oauth2/token
    InFlight: func() int { return int(inFlight.Load()) },
    Logger:   log,
})
go gw.Run(ctx)                                         // reconnects until ctx is done
…
<-ctx.Done()                                           // SIGTERM
gw.Drain(context.Background(), "sigterm")             // then stop the HTTP server
```

Platform packages
-

One package per SQL module of db-platform, in the order of its `create.psql`; the resources are the module's `api.*` functions in the `/api/v2` shape.

| Package | SQL module | Resources |
|---------|------------|-----------|
| `admin` | `admin` | `users` (+`profile`, `iptable`, `groups`/`areas`/`interfaces` memberships, `memberships`, `actions/{action}`), `groups`, `areas` (+`actions/{action}`, `actions/clear`), `area-types`, `interfaces`, `sessions`, `locales` |
| `resource` | `resource` | `resources` |
| `error` | `error` | `errors`, `errors/by-code/{code}` |
| `registry` | `registry` | `registry`, `registry/keys` (+`{id}/path`, `enum`), `registry/values` (+`read`, typed `PUT`), `registry/tree` |
| `log` | `log` | `event-log`, `me/event-log` |
| `api` | `api` | `api-log` |
| `current` | `session`, `current` | `me` (`GET` — the session's area, interface, locale, operating date, user in one object; `PATCH` — `set_session_*`) |
| `workflow` | `workflow` | `entities`, `types`, `classes`, `states`, `state-types`, `actions`, `methods`, `transitions`, `events`, `event-types`, `priorities` |
| `kladr` | `kladr` | `kladr`, `kladr/{id}/history`, `kladr/string` |
| `entity/object` | `entity/object` | `objects`, `objects/{id}/methods`, `objects/{id}/actions/{action}`, `objects/{id}/methods/{method}`, `objects/{id}/access` (`GET` the entries, `PUT` one grant — `api.chmodo`; `access/decode[?userid=]` the bits of one user), `objects/{id}/files` (`GET` list with total, `POST` a JSON array of files — `api.set_object_files_json`, `DELETE` clears; `files/{file}` `GET` with the bytes, `DELETE`), `search`. The rest of the platform's `rest.object` dispatcher (class, type, state and method history, groups, links, data, addresses, geolocation) and `rest.document`/`rest.reference` have no consumer and no form here |
| `notification` | `notification` | `notifications`, `notifications/since`, `notifications/changed` |
| `verification` | `verification` | `verification/codes`, `verification/codes/confirm` |
| `observer` | `observer` | `observer/publishers`, `observer/listeners` (the caller's own session, read only) |

A project's packages follow the same rule in its own module: `entity/object/document/<x>/` for an entity `<x>` of its configuration, listed in its `main` after the platform's.

Configuration
-

The library reads no environment itself; the process does and passes the values on. What a process needs:

| | |
|---|---|
| `platform.Config.Keys` | the secrets of the OAuth2 providers whose tokens the process accepts (`jwt.Keyring`: audience → secret and algorithm), the same keys `AuthServer` signs with |
| `platform.Config.TrustedProxies` | the proxies whose `X-Forwarded-For` is believed (`platform.ParseTrustedProxies("10.0.0.0/8, 172.20.0.1")`). Nil: only the peer is — the client is the **last** element, the one the peer appended (the gateway sends exactly one); with a list the client is the rightmost address not in it, and a peer outside the list is the client itself (nginx `real_ip_recursive`, Express `trust proxy`); an empty list trusts nobody. An element that is not an address stops the walk at the last address read (the proxy that wrote it, or the peer) — never at "no address": a NULL host is "no restriction" to the database's IP tables |
| `pgtx.NewPool(ctx, dsn)` | the database, as the API role of db-platform (not a superuser: a call that works as superuser and fails as the API role is a missing grant) |
| `gatewayclient.Config` | `URL` (`ws://gateway:port/gateway`), `Module`, `Instance`, `Address` or `ListenPort`, `Capacity`, `Token` |

Nodes listen on the internal network only; between the gateway and a module it is HTTP without TLS; cookies never reach a module — `Authorization: Bearer` and `X-Request-Id` do.

Installation
-

Go 1.26 or later. The repository is consumed as a git submodule of a project (`go/platform`), the way db-platform is consumed as `db/sql/platform`:

```bash
git submodule add https://github.com/apostoldevel/go-platform.git platform
```

```go
// go.mod of the project
require github.com/apostoldevel/go-platform v0.0.0
replace github.com/apostoldevel/go-platform => ./platform
```

The project's binary lists its modules and starts the host, the pool and the gateway client:

```go
mods := []platform.Module{
    admin.New(admin.Config{Doer: runner, Logger: log}),
    workflow.New(workflow.Config{Doer: runner, Logger: log}),
    …
    client.New(client.Config{Doer: runner, Logger: log}),   // the project's own
}
handler, err := platform.New(platform.Config{Keys: keys, InFlight: &inFlight}, mods...)
```

**Database.** The db-platform version named in `platform.DBPlatform`, with the `gateway` module (`api.authorize_local`, `api.log_request`) for the session-per-transaction path; without it the library falls back to `api.authorize` and logs nothing.

**Gateway.** [Gateway API](https://github.com/apostoldevel/module-GatewayAPI) in the Apostol worker, with the module's network in its `allowed_cidr`. Without the gateway a module still answers `/api/v2/*` on its address; `cmd/gatewaystub` plays the control plane for a local run:

```bash
go run ./cmd/gatewaystub -addr 127.0.0.1:4978 -secret stub-secret -audience gateway-stub
```

Tests
-

```bash
go build ./... && go vet ./... && gofmt -l . && go test ./... -count=1 -race
```

Integration tests run against a live db-platform database, as the API role, and mint their session through an administrator's login:

```bash
GO_TEST_PG_DSN=postgres://apibot:…@localhost:5432/db \
GO_TEST_ADMIN_DSN=postgres://admin:…@localhost:5432/db \
GO_TEST_DB_PLATFORM_VERSION=$(cat path/to/db-platform/VERSION) \
  go test -tags integration -run Integration ./... -count=1
```

A `VERSION` different from `platform.DBPlatform` is a red test: bump the constant in the commit that re-runs the integration tests, never alone.

[^crm]: **Apostol CRM** — a template project built on the [A-POST-OL](https://github.com/apostoldevel/libapostol) (C++20) and [PostgreSQL Framework for Backend Development](https://github.com/apostoldevel/db-platform) frameworks.
