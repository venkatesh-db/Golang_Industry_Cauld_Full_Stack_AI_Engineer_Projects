# Load-test results

Run the reproducible baseline with:

```shell
RATE=100 DURATION=30s make load
```

The acceptance thresholds are less than 1% failed requests, p95 below 250 ms,
p99 below 750 ms, and more than 99% successful checks. Results depend on the
machine and container resources.

## Verified local results

These results were measured on 2026-07-19 against the complete Docker Compose
stack on a local Docker Desktop development machine. All services exported
metrics and traces during the runs.

| Test | Requests | Throughput | Failures | Average | p95 | Maximum | p99 gate |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| Baseline, 100 RPS for 30 s | 3,000 | 99.99 RPS | 0% | 1.63 ms | 4.94 ms | 34.83 ms | `< 750 ms`, passed |
| Baseline, 1,000 RPS for 30 s | 30,002 | 999.99 RPS | 0% | 0.56 ms | 1.02 ms | 35.11 ms | `< 750 ms`, passed |
| Failure pressure, 20 VUs for 30 s | 3,026 | 97.18 RPS | 0% | 95.86 ms | 7.26 ms | 14.01 s | `< 1,500 ms`, passed |

The failure-pressure maximum is intentionally high: the run held all 10 feed
database connections for 15 seconds while also retaining 64 MiB, leaking 100
bounded goroutines, and injecting a one-second recommendation delay. The service
remained available, and teardown returned retained memory and injected
goroutines to zero.

These are local verification measurements, not a claim of one-billion-request
capacity. Use the capacity model in `ARCHITECTURE.md` with production hardware,
representative payloads, realistic cache hit rates, and a distributed load
generator before making a capacity commitment.
