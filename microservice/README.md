# Resilient order placement orchestration

This repository implements one deliberately narrow, high-value feature for a trading platform: **idempotent order placement with a transactional outbox saga**.

Placing an order is not a single RPC. The platform must durably accept the intent, reserve margin, route the order to an exchange, and release the reservation when an exchange definitively rejects it. Each remote call can time out, repeat, or arrive after a worker crash.

## Delivery contract

```text
client request (account_id + idempotency_key)
        |
        v
 [one local transaction]
 orders + outbox(margin.reserve)
        |
        v
 reserve margin --approved--> outbox(exchange.route) --> accepted
        |                         |
     declined                 definitively rejected
        |                         |
     order rejected       outbox(margin.release) --> reservation released
```

- Replaying the same idempotency key returns the original order. Reusing it with a changed order is rejected.
- Order state changes and successor outbox messages commit in the *same* transaction.
- External calls use the immutable outbox event ID as their idempotency key. This makes at-least-once delivery safe after a worker crash.
- Leased outbox claims prevent concurrent workers from processing an event at the same time; an expired lease may be reclaimed.
- Transient faults back off and retry. If the outcome of a remote operation remains unknown after the retry budget, the order enters `RECONCILIATION_REQUIRED`; the system does **not** release potentially committed margin or assume an exchange rejection.
- Compensation happens only after a definitive exchange rejection, where releasing margin is safe.

The checked-in `MemoryStore` is a deterministic, serializable reference implementation used by tests. The interfaces intentionally map to a production PostgreSQL implementation: a unique `(account_id, idempotency_key)` order index, a durable outbox table, `FOR UPDATE SKIP LOCKED` claiming, and a lease token conditional on every acknowledgement.

Run the test suite with:

```sh
go test ./...
```

## Explore it in a browser

The local demo is an idempotency proof: it shows that an identical retry returns
the original order and that reusing the same key with changed order details is
rejected. The page also explains the database and transactional-outbox rules
behind that guarantee.

```sh
go run ./cmd/order-demo
```

Open `http://localhost:18090`. Set `PORT` to use another available local port.
The UI also runs as a self-contained visual proof when its `index.html` is
opened directly. The server-backed version uses the in-memory reference store
and simulated gateways; it is an executable architecture explainer, not a
production trading UI.
