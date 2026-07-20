# Architecture: accelerate, secure, distribute

This is an edge gateway for a fintech platform: every external request
passes through it before reaching a backend service (payments, ledger,
KYC, rates). It's organized around the three things the brief asked for.

## A note on "millions of requests per second"

That's a claim about a deployed system under load, not about a code
repository. What this repo proves, with `go test -bench` and `-race`:
each component's per-operation cost and correctness under concurrency
(see the benchmark table below). What it does *not* claim to have
proven: an actual sustained multi-million-RPS number, which requires
running compiled binaries on real hardware behind a real load generator
(k6, vegeta, Locust) against a real network path. The honest version of
this document describes the architecture that makes that number
achievable and the design decisions that avoid the failure modes that
usually prevent it — not a benchmark run that didn't happen.

## Accelerate

| Concern | Package | Approach |
|---|---|---|
| Backend lookups (merchant config, exchange rates) shouldn't hit a backend every request | [`internal/cache`](../internal/cache) | Sharded LRU+TTL cache, 256 independent locked segments so unrelated keys never contend on one lock |
| Concurrent misses for the same hot key shouldn't fan out into N backend calls | `cache.GetOrLoad` | Collapses concurrent misses for one key into a single load, at shard granularity |
| Allocation churn on the request hot path is GC pressure that shows up as tail latency | [`internal/pool`](../internal/pool) | `sync.Pool`-backed buffer reuse for proxied response bodies; oversized buffers are dropped, not pooled, so one large payload doesn't bloat steady-state memory |
| A per-key global mutex is the first thing that falls over at high concurrency | `ratelimit`, `cache` | Both shard by key (FNV hash → one of 256 segments) instead of one global lock |
| Backend connections shouldn't be re-established per request | `gateway.New`'s `http.Transport` | Tuned `MaxIdleConnsPerHost`/`IdleConnTimeout` so the reverse proxy reuses persistent connections to backends |

## Secure

| Concern | Package | Approach |
|---|---|---|
| Brute-forced OTPs, scripted payment retries, scraping | [`internal/ratelimit`](../internal/ratelimit) | Sharded token bucket per caller key; background sweeper evicts idle buckets so a public gateway fronting millions of distinct callers doesn't leak memory per abandoned caller |
| Forged/tampered/algorithm-confused bearer tokens | [`internal/auth`](../internal/auth) | HS256-only verification, constant-time signature comparison (`hmac.Equal`), mandatory `exp` claim, explicit rejection of `alg: none` and any non-HS256 algorithm |
| Path traversal, oversized requests, SQLi/XSS-shaped payloads | [`internal/waf`](../internal/waf) | Request-shape validation before routing: body size cap via `http.MaxBytesReader`, URL/header limits, decoded-query pattern checks — explicitly documented as defense-in-depth, not a replacement for backend-side parameterized queries |
| A single dead/slow backend cascading into every other backend | [`internal/breaker`](../internal/breaker) | Per-backend circuit breaker; open state fails fast locally instead of piling up blocked goroutines and exhausted connection-pool slots |

## Distribute

| Concern | Package | Approach |
|---|---|---|
| Spread load across replicas of a backend service | [`internal/loadbalancer`](../internal/loadbalancer) | Round robin, least-connections, weighted-random, and consistent-hash strategies, all skipping unhealthy backends |
| Same account should hit the same replica (cache locality, session affinity) | `loadbalancer.ConsistentHash` | 100 virtual nodes per backend on a hash ring; a key's route only changes for the (1/N) fraction of the ring owned by a backend that becomes unhealthy |
| Route requests to the right backend service by path | [`internal/gateway`](../internal/gateway)'s `Route` table | Longest-prefix-match routing (`/api/payments` → payments pool, `/api/rates` → rates pool), each with its own pool, breaker set, and auth/cache policy |
| A backend that starts failing shouldn't keep receiving full traffic | `gateway.fetch` | Retries a failed attempt (5xx or open breaker) against a different Pick, up to 3 attempts, before giving up |

## How this scales beyond one process

None of the above is enough by itself — a single gateway process, however
fast, is one machine's worth of capacity. The path to "handles a lot of
traffic" is horizontal:

1. **Stateless replicas.** Every piece of state that would normally sit
   in the gateway process (`ratelimit.Limiter`, `cache.Cache`) is
   in-memory and per-instance here, which is correct for a single
   process but means a caller hitting different gateway replicas gets a
   separate rate-limit budget and a separate cache per replica. Both
   packages are built behind an interface-shaped seam (`Limiter.Allow`,
   `Cache.Get/Set`) specifically so the in-memory implementation can be
   swapped for a Redis-backed one (`INCR`+`EXPIRE` for rate limiting,
   Redis itself as the shared cache) without touching `gateway`. Do that
   swap before running more than one replica in production — otherwise
   a caller can get `N×` the intended rate limit by spreading requests
   across `N` replicas.
2. **An L4/L7 load balancer in front of the replicas** (cloud LB, or
   HAProxy/Envoy) distributes inbound connections across gateway
   instances. This repo's `loadbalancer` package is for the
   gateway→backend hop, not the client→gateway hop — that one is
   normally somebody else's infrastructure (a managed LB), not more Go
   code.
3. **Autoscale on the signal that actually predicts saturation** for
   this workload: goroutine count and connection-pool exhaustion
   usually show up before raw CPU does in a proxy, since the work is
   I/O-bound.
4. **Server tuning** (`cmd/gateway/main.go`): `ReadHeaderTimeout` bounds
   slow-header attacks (Slowloris-class), `IdleTimeout` recycles
   keep-alive connections, and the backend-facing `http.Transport` pools
   connections per backend host so a burst of requests doesn't each pay
   a fresh TCP+TLS handshake.
5. **Circuit breakers are per-replica, not shared** — deliberately. A
   breaker existing to fail fast locally when *this* replica's calls to
   a backend are failing doesn't need cross-replica coordination; if
   every replica is independently seeing the same backend fail, they'll
   each trip independently, which is the correct outcome without needing
   a distributed breaker state store.

## Benchmarks

Actual output of `go test -bench=. -benchmem -run=^$ ./...` on this
repo's development machine (Apple M1 Pro, `go1.25.5 darwin/arm64`) —
not estimates:

```
BenchmarkAllow-8                      10,580,917 reps    111.6 ns/op      0 B/op    0 allocs/op   (ratelimit.Allow, steady state)
BenchmarkGet_Hit-8                     6,276,901 reps    189.6 ns/op      0 B/op    0 allocs/op   (cache.Get, hit path)
BenchmarkSet-8                        14,381,397 reps     86.4 ns/op     21 B/op    2 allocs/op   (cache.Set)
BenchmarkGetPut-8                     46,112,978 reps     27.3 ns/op      0 B/op    0 allocs/op   (pool.Get/Put round trip)
BenchmarkWithoutPool-8                 2,163,988 reps    592.9 ns/op   4096 B/op    1 allocs/op   (same workload, fresh allocation instead of pooling)
BenchmarkPick_RoundRobin-8            15,358,130 reps     96.2 ns/op      0 B/op    0 allocs/op   (loadbalancer.Pick)
BenchmarkPick_ConsistentHash-8         8,779,178 reps    137.9 ns/op      0 B/op    0 allocs/op   (ring walk + linear health scan)
BenchmarkGateway_ProxySuccessPath-8       39,018 reps  33,356   ns/op  47,723 B/op  103 allocs/op   (full WAF→routing→proxy chain, in-process httptest backend)
```

`BenchmarkGetPut` vs `BenchmarkWithoutPool` is the one worth reading
carefully: pooling the response buffer is ~20x faster and allocates
nothing, versus one 4096-byte heap allocation per call without it. That
gap is exactly the GC-pressure cost `internal/pool` exists to avoid at
high request rates — and it only shows up correctly in the benchmark
once the buffer is forced to actually escape to the heap (assigned
somewhere the compiler can't prove is dead), which an earlier draft of
this benchmark got wrong: it fed only a computed length out of the loop,
which let escape analysis stack-allocate the un-pooled buffer and made
the two variants look identical. The lesson generalizes: a Go
allocation microbenchmark that doesn't force its result to escape isn't
measuring what it claims to.

`BenchmarkGateway_ProxySuccessPath` is three orders of magnitude slower
than the individual components (33μs vs ~100-200ns) because it's
dominated by `httptest`'s in-process HTTP round trip (real TCP-stack
simulation, header parsing, the actual `httputil.ReverseProxy` copy) —
not gateway logic overhead. It's useful for catching a regression that
adds real per-request cost, not as a measure of gateway-only overhead in
isolation.

These are per-operation microbenchmarks on isolated components, run on
a single developer machine — useful for spotting regressions and for
confirming the sharding/pooling design choices actually pay off, not a
substitute for a real load test of the assembled gateway under
production-shaped traffic and network conditions.
