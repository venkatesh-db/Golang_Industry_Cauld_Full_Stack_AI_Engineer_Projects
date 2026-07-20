-- Read model (CQRS projection of the event stream) — ADR-001 D10
CREATE TABLE IF NOT EXISTS orders (
  order_id       UUID PRIMARY KEY,
  status         TEXT NOT NULL,
  customer_id    TEXT,
  restaurant_id  TEXT,
  restaurant_name TEXT,
  amount         NUMERIC,
  rider_id       TEXT,
  rider_name     TEXT,
  refund_status  TEXT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS order_timeline (
  id         BIGSERIAL PRIMARY KEY,
  order_id   UUID NOT NULL,
  status     TEXT NOT NULL,
  event_type TEXT NOT NULL,
  at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_timeline_order ON order_timeline(order_id);

-- Transactional outbox (ADR-001 D6). Written in the same txn as business state.
CREATE TABLE IF NOT EXISTS outbox (
  id           UUID PRIMARY KEY,
  aggregate_id UUID NOT NULL,
  topic        TEXT NOT NULL,
  msg_key      TEXT NOT NULL,
  payload      JSONB NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  sent_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_outbox_unsent ON outbox(created_at) WHERE sent_at IS NULL;

-- Inbox / idempotency (ADR-001 D5). Unique (group,event) = effectively-once effects.
CREATE TABLE IF NOT EXISTS processed_events (
  consumer_group TEXT NOT NULL,
  event_id       UUID NOT NULL,
  processed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (consumer_group, event_id)
);
