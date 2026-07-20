package domain

import "time"

// EventType is a normalized, provider-agnostic subscription event.
type EventType string

const (
	EventSubscriptionCreated  EventType = "subscription.created"
	EventSubscriptionUpdated  EventType = "subscription.updated"
	EventSubscriptionCanceled EventType = "subscription.canceled"
	EventPaymentFailed        EventType = "payment.failed"
	EventPaymentSucceeded     EventType = "payment.succeeded"
)

// Event is what a provider adapter maps a raw webhook into. ProviderEventID is
// the idempotency key; Subscription carries the provider's desired-state snapshot.
type Event struct {
	ProviderEventID string
	Type            EventType
	Subscription    Subscription
	OccurredAt      time.Time
}

// TargetStatus maps an event to the status it implies. Payment failure implies
// past_due (grace), not an immediate hard cancel.
func (e Event) TargetStatus() Status {
	switch e.Type {
	case EventSubscriptionCanceled:
		return StatusCanceled
	case EventPaymentFailed:
		return StatusPastDue
	case EventPaymentSucceeded:
		return StatusActive
	case EventSubscriptionCreated, EventSubscriptionUpdated:
		if e.Subscription.Status != "" {
			return e.Subscription.Status
		}
		return StatusActive
	default:
		return e.Subscription.Status
	}
}
