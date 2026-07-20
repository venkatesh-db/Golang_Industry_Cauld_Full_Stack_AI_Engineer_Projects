# RailCache

An IRCTC-style **train search & seat-availability** service that demonstrates the
one Redis feature that matters most under Tatkal-scale bursts: **caching the
read-heavy search/availability path** so the same hot query is served sub-ms from
Redis while Postgres (the source of truth) stays shielded — hardened to a
production bar (validation, circuit breaker, stale-while-revalidate, split
listeners, observability).

Go · chi · go-redis · pgx · Postgres · Redis.

## The problem it solves

For every booking, ~50–100 users just *search* and *refresh availability* on the
identical `(from, to, date, class)` tuple. During the 10/11 AM Tatkal window that
means millions of reads on a handful of hot keys in seconds. A relational DB
cannot serve that read volume or survive the lock/scan contention. RailCache puts
Redis in front and coordinates the herd.

## The read path (the whole point)

Freshness is **logical**: the envelope carries a `FreshUntil` timestamp, but the
value physically survives in Redis ~10× longer so it can be served
**stale-while-revalidate**. The distributed lock therefore only matters for cold
starts — hot keys never hard-expire under load.

```
key = avail:v1:{FROM}:{TO}:{DATE}:{CLASS}
GET redis (behind a circuit breaker) →
  FRESH (now < FreshUntil)  → serve                                            [X-Cache: HIT]
  STALE (now ≥ FreshUntil)  → serve stale + one gated background refresh       [X-Cache: STALE]
                              (refresh DB error → keep serving stale)          (stale-if-error)
  MISS (physically gone)    → SET lock NX PX   (distributed fill-lock)
            won  → query Postgres (detached ctx) → SETEX(logical×N) → Lua release [X-Cache: MISS]
            lost → re-read cache → serve when winner fills                        [X-Cache: HIT, suppressed]
                   budget exhausted → singleflight one coalesced DB read          [X-Cache: HIT/MISS]
  ERROR / breaker open      → fail-fast, rate-limited direct Postgres            [X-Cache: FALLBACK]
```

Every request carries `X-Cache`, `X-Data-Age-Seconds`, and `Server-Timing`.

Principal-architect properties, and where each lives:

| Property | Mechanism | File |
|---|---|---|
| Stampede protection | Redis distributed lock (`SET NX PX` + token-checked Lua release) — one DB fill per cold herd | `cache/lock.go`, `search/service.go` |
| Freshness (SWR) | logical `FreshUntil` + physical ×N TTL; serve-stale + gated background refresh; stale-if-error | `search/service.go`, `types.go`, `policy.go` |
| Per-query freshness | Tatkal-window dates get seconds; far dates get minutes | `search/policy.go` |
| Fail-fast Redis | `MaxRetries:-1` + circuit breaker (outage latency ~420ms → ~1ms) | `cache/breaker.go` |
| Boundary defense | class/date/station validation; Redis `maxmemory`+LRU | `search/validate.go`, `stations.go` |
| Detached shared work | fills/writes on `context.WithoutCancel` so a disconnect can't cancel them | `search/service.go` |
| Graceful degradation | Redis outage → Postgres (200, not 5xx); degraded-aware `/readyz` | `search/service.go`, `httpapi/handlers.go` |
| DB protection | per-IP-first rate limiting; Postgres `statement_timeout` + pool gauges | `httpapi/middleware.go`, `store/postgres.go` |
| Security surface | admin/debug/metrics/pprof on a separate internal listener, bearer-auth'd | `httpapi/handlers.go`, `cmd/railcache/main.go` |
| Observability | `X-Cache`/`Server-Timing`, request IDs, latency histogram, gauges, pprof | `metrics/metrics.go`, `httpapi/middleware.go` |
| Herd proof | bounded built-in concurrency load generator | `internal/herd/herd.go` |

Design rationale lives in the feature's ADRs (`adr-001-stampede-and-freshness`,
`adr-002-production-hardening`).

## Run it

```bash
make up      # start Postgres + Redis (docker compose)
make seed    # apply schema.sql + seed.sql
make run     # service on :8080 (public) + :9090 (internal admin/metrics), ADMIN_TOKEN defaults to dev-admin-token
```

Open http://localhost:8080, search **NDLS → BCT**, class **3A**.
First load is `MISS`; refresh → `HIT`; wait past the logical TTL → `STALE` (served
instantly while it revalidates in the background).

### Prove the stampede collapse (admin, internal listener)

```bash
# herd, metrics, and pprof live on :9090 and require the admin token
make herd
# → db_fills_delta ≈ 1, the rest served HIT/suppressed
curl -s localhost:9090/metrics | python3 -m json.tool
```

### Watch graceful fallback

```bash
docker compose stop redis
curl -si "localhost:8080/api/search?from=NDLS&to=BCT&date=$(date -v+2d +%F)&class=3A" | grep X-Cache
# → X-Cache: FALLBACK   (still 200, served from Postgres; ~1ms once the breaker opens)
```

## Configuration (env, all optional)

`HTTP_ADDR`, `ADMIN_ADDR`, `DATABASE_URL`, `REDIS_ADDR`, `ADMIN_TOKEN` (empty ⇒
admin routes disabled), `CACHE_TTL`, `TATKAL_TTL`, `CACHE_TTL_JITTER`, `NEG_TTL`,
`PHYSICAL_TTL_MULTIPLIER`, `SOFT`/`LOCK_TTL`, `FILL_TIMEOUT`, `BREAKER_THRESHOLD`,
`BREAKER_COOLDOWN`, `DB_STATEMENT_TIMEOUT`, `RATE_PER_IP`, `RATE_GLOBAL`,
`REQUEST_TIMEOUT`, `DATE_WINDOW_DAYS`, `HERD_MAX_N`, `HERD_MAX_CONCURRENCY`.
See `internal/config/config.go`.

## Production evolution (noted, not built here)

- **Redis Cluster** for horizontal cache scale + hot-key sharding; **Redlock** if
  a single-node lock's fault tolerance is insufficient.
- The **global rate limit is per-process** — the effective DB budget is
  `RATE_GLOBAL × replicas`; wire it to the deployment or use a shared limiter.
- Swap the hand-rolled metrics for **prometheus/client_golang** 1:1.
- Booking/payment/PNR stay in ACID Postgres transactions — **out of scope** here.
