# Benchmark results

Commit reproducible numbers here after running the load profiles. Template below;
fill from your run (`make load-baseline`, `make bench-vegeta`).

## Environment stamp

- Host: Apple Silicon (Darwin 23.5.0), Docker Desktop 28.3, 7.65 GiB allocated to the VM
- Date: 2026-07-19
- Stack: single-replica compose, `CHAOS_ENABLED=true`, DB_POOL_MAX_CONNS=10
- Seed: 2,000 users · 40,000 posts · ~50,000 edges
- Total stack RAM at rest: **585 MiB** (guardrail 4 GB) — Go services 12–17 MiB each

## Baseline — server-side latency (Prometheus histograms)

Driver: 3,000 requests, 50-way concurrency via curl against `edge-api /feed` (~55 req/s
sustained average over the 1m window). Latency is authoritative server-side
`http_request_duration_seconds` (not client-side).

| Service | p95 | p99 | SLO |
|---|---|---|---|
| edge-api (fan-out of both downstreams) | **93.6 ms** | **221.5 ms** | p95<500ms / p99<1s ✅ |
| feed-service (cache-aside) | 47.9 ms | 162.9 ms | ✅ |
| counter-service (Redis counters) | 21.7 ms | 88.0 ms | ✅ |

Ordering matches the topology: counter (Redis) < feed (cache+DB) < edge (fans out to both).
Feed cache-aside verified: first read `cache_hit:false`, subsequent `cache_hit:true`.

> Note on k6/Vegeta from the host: this machine 403s HTTP to `127.0.0.1` (a local proxy
> intercepts it) and k6 resolves `localhost`→IPv6 which Docker doesn't publish. Run k6
> **inside the compose network** (`docker run --network <net> grafana/k6 ... http://edge-api:8080`)
> or add `127.0.0.1` to the proxy's no_proxy. The committed k6/Vegeta scripts are unchanged;
> only the host networking needs this note.

## Chaos runs — observed signatures (verified live)

| Mode | Metric moved | Alert fired? | Notes |
|---|---|---|---|
| slow-dep 800ms | feed-service p95 → **957 ms** | **HighP95Latency** fired (feed-service + edge-api) ✅ | edge-api inherits the slow downstream — correct propagation |
| db-pool-exhaust | db_pool_acquired → max | DBPoolSaturated (rule fixed to require max>0) | edge-api false-positive found & fixed during verification |
| retry-storm | error_ratio, breaker | HighErrorRatio (wired) | breaker opens after 5 consecutive downstream 5xx |
| goroutine-leak 2000 | go_goroutines | GoroutineLeakSuspected (wired) | unit-tested: leak + release |
| mem-pressure 512MB | go_memstats_alloc_bytes | — (visual) | unit-tested: allocate + reset |

## Alert lifecycle — verified end-to-end (Prometheus → Alertmanager)

| Alert | Trigger | Lifecycle observed | Alertmanager (:9093) |
|---|---|---|---|
| `DBPoolSaturated` | `db-pool-exhaust` on counter-service | `/readyz` 503 → **pending** @10–35s (`for:30s`) → **firing** @45s | **active** |
| `HighErrorRatio` (critical) | `retry-storm` on counter-service | err_ratio 14%→42%→100% → **pending** → **firing** | **active, severity=critical** |
| `HighP95Latency` | `slow-dep` on feed-service | feed p95 → 957ms → firing (feed + edge-api) | active |

Graceful degradation held throughout the retry-storm: `edge-api /feed` kept returning the
feed with `"degraded":["counts"]` (503 only if *all* downstreams fail).

## Trace correlation — verified

- 12+ exemplars carrying `trace_id` on the latency histogram (metric → Tempo).
- A single trace (`b5cfef…`) spans **all three services** (edge-api → feed-service + counter-service).
- Loki streams carry `trace_id` as a label (log → Tempo).
