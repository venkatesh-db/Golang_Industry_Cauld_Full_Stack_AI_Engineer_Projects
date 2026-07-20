# Failure laboratory runbook

The lab is enabled by the local Compose file and disabled by default in application
code. Every experiment is authenticated and bounded. Run only one experiment at
a time while watching Grafana at `http://localhost:13000`.

```shell
export LAB_TOKEN=local-failure-lab-token
```

## Goroutine growth

```shell
curl -X POST 'http://localhost:18080/internal/failure-lab/goroutine-leak?count=100' \
  -H "X-Failure-Lab-Token: $LAB_TOKEN"
```

Expected signal: `go_goroutines` increases and the incident snapshot groups the
blocked lab goroutines together.

## Memory pressure

```shell
curl -X POST 'http://localhost:18080/internal/failure-lab/memory-pressure?mib=64' \
  -H "X-Failure-Lab-Token: $LAB_TOKEN"
```

Expected signal: heap allocation and GC activity increase. The hard default ceiling
is 128 MiB per process.

## PostgreSQL pool exhaustion

```shell
curl -X POST 'http://localhost:18080/internal/failure-lab/db-pool-exhaustion?connections=10&duration_ms=15000' \
  -H "X-Failure-Lab-Token: $LAB_TOKEN"
```

Expected signal: feed latency rises while requests wait for a pool connection.

## Retry storm

```shell
curl -X POST 'http://localhost:18080/internal/failure-lab/retry-storm?attempts=20' \
  -H "X-Failure-Lab-Token: $LAB_TOKEN"
```

Expected signal: one trace contains repeated failed downstream calls and the
recommendation service records 503 traffic.

## Slow dependency

```shell
curl -X POST 'http://localhost:18081/internal/failure-lab/slow-dependency?duration_ms=2000' \
  -H "X-Failure-Lab-Token: $LAB_TOKEN"
```

Expected signal: the failure-lab route p95/p99 rises and its histogram exemplar
links to the matching Tempo trace.

## Inspect and reset

```shell
curl 'http://localhost:18080/internal/failure-lab/status' \
  -H "X-Failure-Lab-Token: $LAB_TOKEN"

curl -X POST 'http://localhost:18080/internal/failure-lab/reset' \
  -H "X-Failure-Lab-Token: $LAB_TOKEN"
```

Always reset retained memory and blocked goroutines after an experiment.
