# subscriptioncore

A provider-agnostic subscription/billing **core** in Go, built as a principal-architect reference.
The domain is pure (no I/O); Postgres, Redis, and Stripe are edges behind interfaces, so the
whole thing compiles and tests green with **zero external infrastructure**.

## Design in one picture

```
Provider ─signed webhook─▶ webhook.Processor
                           verify → dedupe(event id) → order guard → state machine
                           → persist (source of truth) → bust cache
App ─Check()─▶ entitlements.Service
              cache-aside (Redis) → miss/outage → derive from DB  [FAIL-OPEN]
App ─Record()─▶ usage.Meter (counter INCR; durable flush = separate worker)
```

## The patterns this base implements (the whole point)

| Pattern | Where | Why |
|---|---|---|
| **Source of truth vs projection** | `store` (truth) → `domain.DeriveEntitlement` → `cache` | Redis is rebuildable; never authoritative |
| **Idempotent webhooks** | `webhook.Processor` + `WebhookEventRepo.MarkProcessed` | Unique event id = the guarantee; redelivery is a no-op |
| **Out-of-order guard** | `ProviderUpdatedAt` watermark | A late/stale event can't resurrect old state |
| **Explicit state machine** | `domain.statemachine` | Legal transitions only; illegal ones rejected, never applied |
| **Grace policy** | `domain.StatusGrantsAccess` (past_due keeps access) | Failed payment ≠ instant cutoff |
| **Fail-open reads** | `entitlements.Service.Entitlement` | Redis outage degrades latency, not correctness |
| **Hot-write metering** | `usage.Meter` over `Counter` | Per-call DB writes don't scale |
| **Provider port** | `provider.BillingProvider` + `provider/fake` | Vendor swappable; core testable |

## Package map

```
domain/        entities, state machine, entitlement projection   (pure, no I/O)
provider/      BillingProvider port + fake adapter
store/         persistence ports (SubscriptionRepo, WebhookEventRepo, PlanRepo)
store/memory/  in-memory implementation of the store ports
cache/         EntitlementCache port + in-memory impl (can simulate outage)
usage/         Counter port + Meter (metered usage)
entitlements/  request-path service: Check() with cache-aside + fail-open + quota
webhook/       idempotent event Processor
```

## Run

```bash
go test -race -cover ./...
```

## What plugs in next (edges, not core)

The core is done and tested. Production wiring is additive — no domain changes:

1. **Postgres adapter** — implement `store.*` with `pgx` + `sqlc`; add migrations for
   `subscriptions, entitlements, webhook_events, outbox, usage_ledger, plans`.
2. **Redis adapter** — implement `cache.EntitlementCache` and `usage.Counter` with `go-redis`.
3. **Stripe adapter** — implement `provider.BillingProvider` with `stripe-go` (real signature verify + event mapping).
4. **Transactional outbox** — ingress writes an outbox row in the same tx as `MarkProcessed`; a worker drains it into `Processor`.
5. **Reconciliation worker** — periodic `FetchSubscription` diff + repair + cache bust (the pull leg).
6. **Usage flusher** — drain Redis counters into `usage_ledger` on an interval.
7. **HTTP + metrics + config** — chi router, Prometheus counters/histograms, startup secret validation.

See `../.aw_docs/features/subscription-core/` for the full requirements/PRD/spec/ADRs/ERD and the
system-design interview companion.
