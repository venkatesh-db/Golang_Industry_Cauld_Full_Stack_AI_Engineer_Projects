# Principal-architect notes: order placement saga

## Why this boundary

The order service owns the customer-visible order intent and its workflow state. Margin and exchange services own their own ledgers. A distributed transaction across all three would reduce availability and still cannot make an exchange's side effect rollbackable. The correct consistency boundary is a local transaction plus idempotent, durable asynchronous commands.

## Required production persistence

```sql
create table orders (
  id uuid primary key,
  account_id uuid not null,
  idempotency_key text not null,
  request_hash bytea not null,
  instrument text not null,
  side text not null,
  quantity bigint not null check (quantity > 0),
  limit_price numeric(20,4) not null check (limit_price > 0),
  status text not null,
  margin_state text not null,
  reservation_id text,
  exchange_order_id text,
  failure_reason text,
  created_at timestamptz not null,
  updated_at timestamptz not null,
  unique (account_id, idempotency_key)
);

create table outbox_events (
  id uuid primary key,
  aggregate_id uuid not null references orders(id),
  kind text not null,
  payload jsonb not null,
  state text not null,
  attempts integer not null default 0,
  available_at timestamptz not null,
  lease_token uuid,
  lease_until timestamptz,
  last_error text,
  created_at timestamptz not null,
  published_at timestamptz
);
create index outbox_claim_idx on outbox_events (state, available_at)
  where state in ('PENDING', 'RETRY', 'PROCESSING');
```

Workers should claim batches with `FOR UPDATE SKIP LOCKED`, set a fresh lease token and lease expiry, then commit before making the remote call. Completion, the order transition, and emission of the next event must happen in one subsequent transaction and be guarded by `WHERE lease_token = $token`.

## Failure policy

| Situation | Action |
| --- | --- |
| Process dies before remote call | Lease expires; another worker retries. |
| Process dies after remote call | The same external idempotency key is retried. |
| Margin declines | Reject locally; no reservation exists to release. |
| Exchange definitively rejects | Mark rejected and atomically enqueue `margin.release`. |
| Transport timeout / 5xx | Retry with capped exponential backoff. |
| Retry budget exhausted | Mark `RECONCILIATION_REQUIRED`; reconcile against the external service before manual or automatic repair. |

The reconciliation state is deliberate. Treating an ambiguous exchange timeout as a rejection and releasing funds can create an unfunded live position.

## Operational controls

- Alert on outbox age, lease expiry rate, retry count, and `RECONCILIATION_REQUIRED` count.
- Keep inbound idempotency records at least as long as clients can retry, and retain external idempotency keys according to the exchange contract.
- Use a FIFO partition key of `account_id` if the risk engine requires account-level command ordering.
- Audit every terminal transition with actor, reason, and correlation ID; redact client identifiers from logs.
