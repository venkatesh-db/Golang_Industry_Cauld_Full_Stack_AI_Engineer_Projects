# secure-multitenant-go

A compact, production-shaped Go module demonstrating five security & testing
capabilities a principal architect would ship for a Facebook-scale multi-tenant
service.

| Capability | Package | What it does |
|---|---|---|
| **TLS 1.2+ encryption** | `internal/httpx` | Server config with a TLS 1.2 floor, 1.3 preferred, forward-secret AEAD suites, production timeouts. A real handshake test proves a <1.2 client is rejected. |
| **AES-256 encryption** | `internal/cryptox` | AES-256-GCM seal/open with per-call random nonce and tenant-bound associated data (AAD), so a blob sealed for one tenant won't open in another's context. |
| **Multi-tenant `tenant_id` scoping** | `internal/tenant`, `internal/pgstore` | Tenant id flows via header → context → every store call. Cross-tenant reads return `ErrNotFound` (existence is not leaked). Two backends behind one `tenant.Repository` interface: in-memory + Postgres. |
| **RBAC** | `internal/rbac` | Static role→permission matrix (`owner`/`admin`/`billing_viewer`) + HTTP middleware enforcing a required permission per route (401 missing / 403 insufficient). |
| **Table-driven unit tests** | `*_test.go` | Every package is covered with idiomatic Go table-driven tests, including a live TLS handshake and cross-tenant isolation cases. |

## Layout

```
cmd/server/main.go        wires all five together behind one HTTPS endpoint
internal/cryptox/         AES-256-GCM
internal/tenant/          tenant context, middleware, Repository iface, in-memory store
internal/rbac/            role/permission matrix + middleware
internal/httpx/           TLS 1.2+ server config
internal/pgstore/         Postgres-backed Repository + embedded migration + RLS
```

## Persistence layer

`tenant.Repository` has two implementations, chosen at boot by `DATABASE_URL`:

- **In-memory** (`tenant.Store`) — default; zero-dependency, used by the unit suite.
- **Postgres** (`pgstore.Store`) — `pgx/v5` pool. Tenant isolation is enforced
  **twice**: an explicit `WHERE tenant_id = $1` on every query, *and* Row-Level
  Security. Each operation runs in a transaction that binds
  `set_config('app.current_tenant', <tid>, true)`, activating a `FORCE ROW LEVEL
  SECURITY` policy — so even a query that forgot its `WHERE` clause cannot cross
  tenants. The composite PK `(tenant_id, id)` gives each tenant its own id namespace.

Migrations are embedded (`//go:embed`) and applied idempotently by
`pgstore.Migrate`.

### Run the Postgres integration tests

```bash
createdb app_test
DATABASE_URL='postgres://localhost:5432/app_test' go test ./internal/pgstore -v
```

Without `DATABASE_URL` these tests skip, keeping the unit suite hermetic.

## Run the tests

```bash
cd secure-multitenant-go
go vet ./...
go test ./... -v
```

## Run the server

Provide a TLS cert/key (e.g. via `mkcert` or `openssl`) as `server.crt` /
`server.key`, then:

```bash
go run ./cmd/server
# request needs both headers:
#   X-Tenant-ID: tenant-a
#   X-Role: admin   (billing_viewer would get 403 on POST /records)
```

## Notes for production

- The AES key is generated at boot here only so the demo is standalone — source
  it from a KMS / secret manager and support key rotation.
- `X-Role` is a stand-in for a verified JWT/session claim; the middleware shape
  is unchanged when the role comes from a validated token.
- The in-memory `tenant.Store` mirrors the mandatory `WHERE tenant_id = $1`
  predicate every SQL query must carry in a real datastore.
