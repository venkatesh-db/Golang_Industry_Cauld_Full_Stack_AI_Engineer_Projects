# TTD virtual queue

A small, dependency-free Go service that demonstrates a safe virtual-queue
workflow:

- Redis atomically creates short-lived holds and enforces slot capacity.
- A Redis sorted set is the timer index; a worker releases expired holds.
- The timer worker retains active holds until it releases their capacity; it
  applies a TTL only after marking a hold expired.
- Confirmed bookings are appended and `fsync`ed to a local durable log before
  their Redis state is marked confirmed. Replace this store with a transactional
  database repository in production.

## Run

Start Redis, then run:

```sh
REDIS_ADDR=localhost:6379 go run ./cmd/queue-server
```

The service listens on `:8080`. Set capacity for a darshan window, create a
short-lived hold, and confirm it:

```sh
curl -X PUT localhost:8080/slots/2026-08-01T09:00/capacity \
  -H 'content-type: application/json' -d '{"capacity":100}'

curl -X POST localhost:8080/holds -H 'content-type: application/json' \
  -d '{"slot":"2026-08-01T09:00","visitor_id":"v-1001","hold_seconds":300}'

curl -X POST localhost:8080/holds/HOLD_ID/confirm
```

`data/bookings.jsonl` is created at runtime. The timer worker polls every
second by default; configure `TIMER_POLL_INTERVAL` with a Go duration such as
`500ms` or `2s`.

## Why the timer exists

The worker releases a hold when its confirmation deadline passes. Without it,
an abandoned payment flow keeps consuming capacity and the queue appears full.
The sorted set gives the worker a queryable deadline list; relying on Redis
key-expiry notifications alone is unsafe because delivery is best effort.

The log is a compact demo persistence layer. In production, replace it with a
database transaction/outbox around payment confirmation; Redis coordinates
short-lived contention and expiry only.
