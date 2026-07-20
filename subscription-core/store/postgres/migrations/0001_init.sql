-- Schema for the subscription core source of truth.
-- Idempotent: safe to run on every startup.

CREATE TABLE IF NOT EXISTS plans (
    id            TEXT PRIMARY KEY,
    tier          TEXT NOT NULL,
    features      JSONB NOT NULL DEFAULT '{}'::jsonb,
    seat_included INT  NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS subscriptions (
    id                       TEXT PRIMARY KEY,
    customer_id              TEXT NOT NULL,
    provider_subscription_id TEXT NOT NULL UNIQUE,
    plan_id                  TEXT NOT NULL,
    status                   TEXT NOT NULL,
    seat_count               INT  NOT NULL DEFAULT 0,
    current_period_start     TIMESTAMPTZ NOT NULL,
    current_period_end       TIMESTAMPTZ NOT NULL,
    cancel_at_period_end     BOOLEAN NOT NULL DEFAULT FALSE,
    trial_end                TIMESTAMPTZ NOT NULL,
    -- ordering watermark for the out-of-order webhook guard
    provider_updated_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_customer
    ON subscriptions (customer_id);

-- Idempotency ledger: the UNIQUE PK is the webhook dedupe guarantee.
CREATE TABLE IF NOT EXISTS webhook_events (
    provider_event_id TEXT PRIMARY KEY,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
