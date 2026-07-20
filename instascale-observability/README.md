# InstaScale — Observability & Failure Lab

An Instagram-shaped, read-heavy Go microservice slice you can **boot, load, break, and
recover live** — built to *demonstrate* SRE / distributed-systems competence in an
interview, not to run in production. (It ships failure injectors and unsafe knobs by
design — never deploy it to real traffic.)

## What's inside

| Layer | Components |
|---|---|
| Services | `edge-api` (BFF/gateway) → `feed-service` (fan-out-on-read) + `counter-service` (hot counters) |
| Data | Postgres (source of truth) + Redis (cache-aside + counters) |
| Observability (LGTM) | Prometheus (metrics) · Tempo (traces) · Loki (logs) · Grafana (single pane) · OpenTelemetry Collector |
| Resilience | per-call deadlines, bounded jittered retries, circuit breaker (ADR-004) |
| Failure lab | goroutine leak · DB pool exhaustion · retry storm · slow dependency · memory pressure |
| Load | k6 (baseline/spike/write) + Vegeta (independent benchmark) |

Architecture, capacity math, SLOs, and failure-recovery runbooks live in
[`ARCHITECTURE-instascale.md`](ARCHITECTURE-instascale.md).

## Boot (one command)

```bash
make up          # docker compose up -d --build
```

Then open:
- **Grafana** → http://localhost:3000 (anonymous admin) → dashboard *InstaScale — RED + Resources*
- **Prometheus** → http://localhost:9090 · **Alertmanager** → http://localhost:9093
- **edge-api** → http://localhost:8080/feed/1

Generate traffic: `make load-baseline` (needs [k6](https://k6.io)).

## The 5 failure modes

Each is `CHAOS_ENABLED`-gated, reversible, and produces a **distinct telemetry signature**.

| Make target | Endpoint | Watch for |
|---|---|---|
| `make chaos-goroutine` | `POST edge-api/chaos/goroutine-leak?n=` | *Goroutines* panel ramps monotonically; RSS climbs |
| `make chaos-dbpool` | `POST counter-service/chaos/db-pool-exhaust?n=` | *DB pool acquired == max*; `readyz` degrades; p99 up |
| `make chaos-retrystorm` | `POST counter-service/chaos/retry-storm` | error ratio spikes, downstream rate multiplies, **breaker opens** |
| `make chaos-slowdep` | `POST feed-service/chaos/slow-dep?ms=` | **p95/p99 latency alert fires**; slow span visible in the trace |
| `make chaos-mem` | `POST edge-api/chaos/mem-pressure?mb=` | *Heap alloc* + RSS climb, GC pause up |
| `make chaos-reset` | `DELETE /chaos/reset` (all) | everything returns to health |

## Trace-ID correlation (the headline)

1. `make load-baseline`, then in Grafana open the **p95 latency** panel — the pink diamonds are
   **exemplars**. Click one → jumps to the exact **Tempo** trace (metric → trace).
2. In that trace, a span's **Logs** link opens **Loki** filtered by `trace_id` (trace → logs).
3. Or start in Loki (`{service="edge-api"} | json`), click a line's `trace_id` field →
   the same Tempo trace (log → trace, ≤ 2 clicks).

One `trace_id` stitches metrics ↔ traces ↔ logs across all three services.

## Layout

```
services/{edge-api,feed-service,counter-service}/   # cmd/ + internal/
internal/obs/      # metrics, tracing, logging, middleware, resilient http client
internal/chaos/    # the 5 injectors + gated /chaos routes
deploy/            # prometheus, tempo, loki, otel, grafana, alertmanager
migrations/        # schema + seed
loadtest/          # k6 + vegeta + committed results
```

## Guardrails

Single replicas, short retention (Prom 6h / Tempo 1h / Loki 2h), Redis capped at 256MB →
target ≤ ~4GB total, boot < 90s. Verify with `make ps`.
