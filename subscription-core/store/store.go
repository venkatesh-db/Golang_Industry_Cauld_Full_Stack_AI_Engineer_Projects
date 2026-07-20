// Package store defines the persistence ports (source of truth). A Postgres
// implementation drops in behind these interfaces without touching the core.
package store

import (
	"context"
	"errors"

	"subscriptioncore/domain"
)

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("not found")

// SubscriptionRepo persists subscriptions.
type SubscriptionRepo interface {
	GetByProviderID(ctx context.Context, providerSubID string) (domain.Subscription, error)
	GetBySubject(ctx context.Context, subjectID string) (domain.Subscription, error)
	Upsert(ctx context.Context, s domain.Subscription) error
}

// WebhookEventRepo enforces webhook idempotency via a unique event id.
type WebhookEventRepo interface {
	// MarkProcessed records the provider event id and reports whether this is
	// the first time it was seen. In Postgres this is INSERT ... ON CONFLICT
	// DO NOTHING; the unique constraint IS the idempotency guarantee.
	MarkProcessed(ctx context.Context, providerEventID string) (firstTime bool, err error)
}

// PlanRepo resolves plans/prices.
type PlanRepo interface {
	Get(ctx context.Context, planID string) (domain.Plan, error)
}
