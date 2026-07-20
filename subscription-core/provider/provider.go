// Package provider defines the BillingProvider port. The core depends only on
// this interface; a Stripe/Paddle/etc. adapter implements it.
package provider

import (
	"errors"

	"subscriptioncore/domain"
)

// ErrInvalidSignature is the shared sentinel every adapter must return when a
// webhook signature fails verification, so callers (HTTP ingress) can map it to
// a 400 without depending on a concrete adapter.
var ErrInvalidSignature = errors.New("provider: invalid webhook signature")

// BillingProvider isolates the core from a specific billing vendor.
type BillingProvider interface {
	// VerifyAndParse verifies the webhook signature and maps the raw payload to
	// a normalized domain event. It must reject invalid signatures.
	VerifyAndParse(payload []byte, signature string) (domain.Event, error)
	// FetchSubscription returns the provider's authoritative subscription state,
	// used by the reconciliation job and sync-on-read.
	FetchSubscription(providerSubID string) (domain.Subscription, error)
}
