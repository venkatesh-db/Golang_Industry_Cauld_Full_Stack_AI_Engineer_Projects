// Package domain holds the provider-agnostic subscription core: entities, the
// state machine, and the entitlement projection. It has no I/O and no vendor
// dependencies, so it is fully unit-testable in isolation.
package domain

import "time"

// Status is the lifecycle state of a subscription.
type Status string

const (
	StatusTrialing Status = "trialing"
	StatusActive   Status = "active"
	StatusPastDue  Status = "past_due"
	StatusCanceled Status = "canceled"
	StatusPaused   Status = "paused"
)

// Subscription is the durable source-of-truth record (mirrored in Postgres).
type Subscription struct {
	ID            string
	CustomerID    string // the subject key the app checks entitlement against
	ProviderSubID string
	PlanID        string
	Status        Status
	SeatCount     int

	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
	CancelAtPeriodEnd  bool
	TrialEnd           time.Time

	// ProviderUpdatedAt is the ordering watermark. Events older than the stored
	// value are ignored (out-of-order guard).
	ProviderUpdatedAt time.Time
}
