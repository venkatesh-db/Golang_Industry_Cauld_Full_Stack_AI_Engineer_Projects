# fintech-gateway-go

A reference edge gateway for a fintech platform: accelerate (caching,
connection/buffer pooling), secure (rate limiting, JWT auth, WAF-lite),
distribute (load balancing, circuit breaking, path routing) — in Go,
with tests (including `-race`) and benchmarks proving each claim.

See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the design
rationale, the scale-out story, and actual benchmark numbers — including
an honest note on what "millions of requests per second" would actually
take to prove versus what this repo demonstrates.

```
fintech-gateway-go/
├── go.mod
├── cmd/
│   └── gateway/main.go        # runnable gateway against fake in-process backends
├── internal/
│   ├── ratelimit/              # sharded token bucket, memory-bounded via background sweeper
│   ├── cache/                  # sharded LRU+TTL, GetOrLoad collapses concurrent misses per key
│   ├── pool/                   # sync.Pool buffer reuse for the request hot path
│   ├── breaker/                # per-backend circuit breaker (closed/open/half-open)
│   ├── loadbalancer/           # round robin, least-connections, weighted-random, consistent hash
│   ├── auth/                   # HS256 JWT verification middleware
│   ├── waf/                    # request-shape validation: size caps, path traversal, injection heuristics
│   └── gateway/                # composes everything into one request pipeline
└── docs/
    └── ARCHITECTURE.md
```

## Running it

```bash
go build ./...
go vet ./...
go test -race ./...
go test -bench=. -benchmem -run=^$ ./...
go run ./cmd/gateway   # listens on :8080 against fake in-process backends
```

## Request pipeline

```
request → WAF → route match → rate limit → (auth, if the route requires it)
        → cache lookup (GET + cacheable routes only)
        → circuit breaker + load balancer → backend, with retry on 5xx/open breaker
```

Every stage that can leak (goroutines, memory) has a lifecycle: the rate
limiter's idle-bucket sweeper and the load balancer's health checker are
both `Close()`-able background goroutines, matching the discipline
established in the companion [fintech-ledger-go](../fintech-ledger-go)
project.

## Honesty notes

- The JWT verifier is HS256-only, hand-rolled to make explicit what a
  correct minimal verifier does (pinned algorithm, constant-time
  signature comparison, mandatory expiry). Production systems should use
  a vetted library for full claim support, key rotation, and asymmetric
  algorithms — see the package doc comment in
  [`internal/auth/jwt.go`](internal/auth/jwt.go).
- The WAF is explicitly defense-in-depth. It rejects obviously hostile
  request shapes before they cost a backend connection; it is not a
  substitute for parameterized queries at the data layer, and its
  injection heuristics will both miss real attacks and flag legitimate
  input.
- Every in-memory component (`ratelimit.Limiter`, `cache.Cache`) is
  per-process. Running more than one gateway replica in production means
  swapping these for a shared backing store (Redis) first — see
  `docs/ARCHITECTURE.md`'s "How this scales beyond one process" section.
- Benchmarks in `docs/ARCHITECTURE.md` are real `go test -bench` output
  from one development machine, not a claim about production RPS. See
  that doc's opening section for what would actually be needed to prove
  a throughput number.
