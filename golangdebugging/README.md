# Production incident snapshots for Go

This repository now includes a three-service production observability lab. The
original diagnostics and metrics packages remain reusable on their own; the full
stack adds PostgreSQL, Redis, OpenTelemetry/Tempo traces, Prometheus, Grafana,
guarded failure experiments, and k6 load tests.

## Full stack quick start

```shell
docker compose up -d --build
./scripts/smoke.sh
```

Open:

- Feed API: `http://localhost:18080/v1/feed?user_id=1`
- Recommendation API: `http://localhost:18081/v1/recommendations?user_id=1`
- Profile API: `http://localhost:18082/v1/profiles/1`
- Prometheus: `http://localhost:19090`
- Grafana: `http://localhost:13000` (`admin` / `admin` for the local lab)
- Tempo: available through Grafana Explore

Run normal and adverse load profiles:

```shell
RATE=100 DURATION=30s make load
make failure-load
```

Architecture, capacity calculations, SLOs, and recovery decisions are documented
in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md). Controlled failure commands
are in [`docs/FAILURE_LAB.md`](docs/FAILURE_LAB.md).

The most useful production-debugging feature is a **single, protected incident
snapshot**: it correlates the latest error logs, runtime memory/scheduler health,
and an optional grouped goroutine dump at the moment an incident is happening.

It answers the first high-value triage questions without logging into multiple
systems: _what just failed, is the process under memory or goroutine pressure,
and where is work waiting?_ It is deliberately bounded and opt-in for expensive
stack collection so it remains safe when the service is already degraded.

## Integration

```go
import (
    "log/slog"
    "net/http"
    "os"

    "golangdebugging/diagnostics"
)

events := diagnostics.NewEventBuffer(250)
logger := slog.New(diagnostics.CapturingHandler(
    slog.NewJSONHandler(os.Stdout, nil),
    events,
    slog.LevelError, // retain errors only; avoid retaining sensitive info
))

snapshots := diagnostics.NewService(diagnostics.Options{
    Events:             events,
    MaxEvents:          50,
    MaxStackBytes:      1 << 20,
    MaxGoroutineGroups: 20,
})

// Mount this only on a private/internal router. Reuse your existing
// authentication instead of the example token check.
mux.Handle("GET /internal/diagnostics/snapshot", diagnostics.NewHandler(diagnostics.HTTPOptions{
    Service:           snapshots,
    Authorize:         func(r *http.Request) bool { return internalAuth(r) },
    AllowGoroutines:   true,
    RequestsPerMinute: 6,
    MaxTrackedClients: 1024, // bounds rate-limit state under hostile traffic
}))
```

During an incident, call:

```text
GET /internal/diagnostics/snapshot
GET /internal/diagnostics/snapshot?goroutines=true
```

The second request is rate limited and serialized. Its stacks are aggregated by
identical stack/state, making deadlocks, connection-pool waits, unbounded worker
growth, and hot retry loops immediately visible. The JSON response is explicitly
`Cache-Control: no-store`.

## Operating guidance

- Keep the route private and require existing service-to-service authentication.
- Retain only `ERROR` and `WARN` records by default; never add secrets to log fields.
- Send application metrics to Prometheus/OpenTelemetry for time-series trend and
  alerting. The snapshot is the incident-time correlation layer, not a metrics
  replacement.
- Include a request/trace ID in every application log. The returned event list
  then links directly to distributed traces and downstream-service logs.
- Sample successful access logs with `ACCESS_LOG_SAMPLE_RATIO`; 5xx requests are
  always logged. The code defaults to 1%, while Compose uses 100% so correlation
  is visible during the lab.

Run the verification suite with `go test ./...`.

## High-volume request metrics

For 1–100 crore requests per day, use the `telemetry` package to expose
Prometheus/OpenMetrics metrics. Unlike incident snapshots, these metrics are
continuously scraped and stored by a metrics backend; they never keep one
record per request in application memory.

```go
import (
    "github.com/prometheus/client_golang/prometheus"
    "golangdebugging/telemetry"
)

registry := prometheus.NewRegistry()
metrics, err := telemetry.New(telemetry.Config{
    Routes: []string{
        "/v1/feed",
        "/v1/posts/{id}",
    },
    Dependencies: map[string][]string{
        "postgres": {"read_feed", "write_post"},
        "redis":    {"get_feed_cache"},
    },
    Registerer: registry,
    Gatherer:   registry,
})
if err != nil {
    return err
}

internalMux.Handle("GET /metrics", metrics.Handler())
apiMux.Handle("GET /v1/feed", metrics.Wrap("/v1/feed", feedHandler))
```

The middleware provides these four metric families:

```text
http_requests_total{route,method,status}
http_request_duration_seconds{route,method,status}
in_flight_requests{route}
dependency_duration_seconds{dependency,operation,status}
```

Every `route`, `dependency`, and `operation` value must be configured at
startup. If code supplies an unknown value, it is recorded as `unknown` rather
than creating a new time series. Never use raw paths, tenant IDs, user IDs,
order IDs, trace IDs, or error messages as labels.

## Run the demo service

The repository includes a small integration server that combines both features.
It requires a diagnostics token rather than shipping with a hard-coded secret:

```shell
DIAGNOSTICS_TOKEN=local-demo-token go run ./cmd/demo
```

In another terminal:

```shell
curl -i http://localhost:8080/healthz
curl http://localhost:8080/v1/feed
curl 'http://localhost:8080/v1/feed?fail=true' -H 'X-Trace-ID: demo-trace-42'
curl http://localhost:8080/metrics
curl 'http://localhost:8080/internal/diagnostics/snapshot?goroutines=true' \
  -H 'X-Diagnostics-Token: local-demo-token'
```
