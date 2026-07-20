# fintech-gateway-go — 3-Minute Interview Walkthrough

> Run: `go test ./...` (8 packages green)

**One-liner:** "An API gateway in Go — the resilience and traffic-management layer that sits in front of backends. Its job is to fail fast, spread load, and shed abuse *before* a request ever costs a backend connection."

## The 90-second architecture story
A request hits the edge → **auth** verifies the JWT before it goes near a backend → **WAF** rejects malformed/hostile shapes → **rate limit** sheds abuse per-key → the **load balancer** picks a healthy backend via a **consistent-hash ring** → the **circuit breaker** fails fast if that backend is unhealthy → **buffer pooling** keeps the hot path allocation-free.

## Point-at-the-line moments

**1. Circuit breaker — `internal/breaker/breaker.go`**
"Per-backend breaker: after N consecutive failures it trips **open** and fails immediately instead of piling up slow doomed calls that exhaust the connection pool and block goroutines — which is exactly how one bad backend cascades into all of them. Closed → Open → Half-Open with bounded trial requests so a still-broken backend isn't re-flooded." Answers: *circuit breaker, cascading failure, bulkhead, resilient calls to a flaky dependency.*

**2. Consistent-hash ring — `internal/loadbalancer/ring.go`**
"Same key (account ID) always routes to the same backend, preserving per-instance cache locality. 100 virtual nodes per backend smooth the distribution; adding a node reshuffles only ~1/N of keys instead of everything." Answers: *consistent hashing, minimize reshuffle, sticky routing, cache locality.*

**3. Sharded rate limiter — `internal/ratelimit/limiter.go`**
"Per-key token bucket, but sharded across 256 independent maps each with its own mutex. One global mutex is the first thing to fall over — every request across every key serializes on the same lock. Sharding means unrelated callers never contend." Answers: *rate limiting, absorbing spikes, lock contention, protecting an API.*

**4. Health checks with a real lifecycle — `internal/loadbalancer/health.go` + `pool.go`**
"A background checker probes each backend on an interval and flips its health flag; unhealthy backends drop out of rotation and drain. It has an explicit `Close` — every started goroutine must be stoppable or it leaks for the process lifetime." Answers: *health vs readiness, draining a node, L4/L7 LB, service discovery basics.*

**5. Buffer pooling on the hot path — `internal/pool/buffer.go`**
"`sync.Pool` reuses byte buffers across requests. At high rps, allocate-and-discard-per-request is GC pressure that shows up directly as **tail latency** — pooling lets the runtime reuse buffers already sized for the common case." Answers: *GC pressure, p99 vs p50, reducing tail latency, optimizing a 50k-rps hot path.*

**6. Edge auth done correctly — `internal/auth/jwt.go`**
"Minimal HS256 verifier that makes the non-negotiables explicit: **pin the algorithm** (defeats alg=none), **constant-time signature compare** (defeats timing attacks), and **require + check expiry**. Comments flag that production wants a vetted lib with kid/JWKS rotation and RS256." Answers: *validate at the boundary, least privilege, auth pitfalls.*

**7. WAF as defense-in-depth — `internal/waf/waf.go`**
"Size caps, path-traversal rejection, injection heuristics — explicitly *not* the defense. The code itself documents that any signature list both misses real attacks and flags legit input (a payment note containing 'SELECT'), so backends must still parameterize independently." Answers: *defense in depth, why input validation belongs at multiple layers.* Shows judgment, not just a feature.

## Follow-ups you can now field
- *"One service degrades and takes others down — prevent it?"* → per-backend breaker + bulkhead isolation; the ring keeps blast radius to one backend's keys.
- *"How do you reduce p99 without hurting p50?"* → kill allocation churn on the hot path (buffer pool), fail-fast on unhealthy backends so slow calls don't queue.
