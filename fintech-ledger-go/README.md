# fintech-ledger-go

A reference Go project demonstrating the real Indian-fintech bug/design/
judgment scenarios below, each as working, tested code — not a writeup of
a hypothetical fix.

```
fintech-ledger-go/
├── go.mod
├── cmd/
│   └── demo/main.go          # wires every package together, runs once, prints results
├── internal/
│   ├── money/                # int64 paise — no float64 money arithmetic
│   ├── ledger/                # Account + Transfer, deadlock-safe via ordered locking
│   ├── wallet/                # mutex-guarded balance cache (race-safe), eventually consistent by design
│   ├── reconciliation/        # bounded worker pool for the settlement batch, no goroutine leak
│   ├── gateway/                # payment gateway client with capped exponential-backoff retry
│   ├── idempotency/            # idempotency key store (Reserve/Complete/Release)
│   ├── payment/                # Debit API — idempotent, safe against client retries
│   ├── saga/                   # wallet→bank withdrawal as a saga with compensations, not 2PC
│   └── partition/              # per-account shard: single owner goroutine, consistent-hash ring
└── docs/
    ├── DESIGN.md              # the 5 design-level gaps and their fixes, with file references
    └── JUDGMENT.md            # the 3 judgment-call case studies
```

## Scenario → file map

| # | Category | Problem | Where it's solved |
|---|----------|---------|--------------------|
| 1 | CODE | Goroutine leak in the settlement/reconciliation worker | [`internal/reconciliation/worker.go`](internal/reconciliation/worker.go) |
| 2 | CODE | Data race on in-memory wallet-balance cache | [`internal/wallet/cache.go`](internal/wallet/cache.go) |
| 3 | CODE | Deadlock from inconsistent lock ordering | [`internal/ledger/account.go`](internal/ledger/account.go) (`Transfer`) |
| 4 | CODE | Floating-point money arithmetic | [`internal/money/money.go`](internal/money/money.go) |
| 5 | CODE | Unbounded retry loop on a payment gateway call | [`internal/gateway/client.go`](internal/gateway/client.go) |
| 6 | DESIGN | No idempotency key on payment APIs | [`internal/idempotency/`](internal/idempotency/), [`internal/payment/debit.go`](internal/payment/debit.go) |
| 7 | DESIGN | Distributed lock instead of DB-enforced correctness | [`docs/DESIGN.md`](docs/DESIGN.md#distributed-lock-instead-of-db-enforced-correctness) |
| 8 | DESIGN | 2PC across bank + wallet instead of saga | [`internal/saga/withdraw.go`](internal/saga/withdraw.go) |
| 9 | DESIGN | No per-account partitioning | [`internal/partition/shard.go`](internal/partition/shard.go) |
| 10 | DESIGN | Eventual consistency applied to the ledger | [`docs/DESIGN.md`](docs/DESIGN.md#eventual-consistency-applied-to-the-ledger) |
| 11 | JUDGMENT | Eventual consistency to cut latency → moving one read path back to strong consistency | [`docs/JUDGMENT.md#1`](docs/JUDGMENT.md) |
| 12 | JUDGMENT | A pressure-driven retry "fix" causing a double-charge storm | [`docs/JUDGMENT.md#2`](docs/JUDGMENT.md) |
| 13 | JUDGMENT | What to reconcile-after vs guarantee-live | [`docs/JUDGMENT.md#3`](docs/JUDGMENT.md) |

## Running it

```bash
go build ./...
go vet ./...
go test -race ./...
go run ./cmd/demo
```

Every "CODE" fix has a test that fails without the fix and passes with
it (deadlock timeout, `-race`-detected data race, goroutine-count
assertion, balance-conservation assertion, bounded-attempt-count
assertion). Every "DESIGN" fix has a test proving the specific incident
it prevents (double-debit on retry, compensation restoring balance after
a failed bank leg, no lost updates under concurrent per-account access).
