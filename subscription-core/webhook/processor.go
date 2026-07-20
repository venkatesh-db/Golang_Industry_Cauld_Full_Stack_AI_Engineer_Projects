// Package webhook processes provider events idempotently, guards ordering, runs
// the state machine, persists to the source of truth, and busts the cache.
package webhook

import (
	"context"
	"errors"

	"subscriptioncore/cache"
	"subscriptioncore/domain"
	"subscriptioncore/provider"
	"subscriptioncore/store"
)

// Result describes the outcome of handling one event.
type Result string

const (
	ResultProcessed Result = "processed"
	ResultDuplicate Result = "duplicate"
	ResultStale     Result = "stale_ignored"
	ResultIllegal   Result = "illegal_transition"
)

// Processor is the event-processing worker. In production it consumes from a
// transactional outbox; here it is called directly.
type Processor struct {
	prov   provider.BillingProvider
	subs   store.SubscriptionRepo
	events store.WebhookEventRepo
	cache  cache.EntitlementCache
}

// NewProcessor wires the processor.
func NewProcessor(p provider.BillingProvider, subs store.SubscriptionRepo, events store.WebhookEventRepo, c cache.EntitlementCache) *Processor {
	return &Processor{prov: p, subs: subs, events: events, cache: c}
}

// Handle verifies the signature, dedupes on the provider event id, applies the
// out-of-order guard and state machine, persists, and busts the cache.
//
// Idempotency note: MarkProcessed runs first, so a redelivery is a fast no-op.
// In production this and the Upsert share one transaction (outbox pattern), so
// a crash between them cannot mark-without-applying; reconciliation is the
// backstop if it ever does.
func (p *Processor) Handle(ctx context.Context, payload []byte, signature string) (Result, error) {
	evt, err := p.prov.VerifyAndParse(payload, signature)
	if err != nil {
		return "", err
	}

	first, err := p.events.MarkProcessed(ctx, evt.ProviderEventID)
	if err != nil {
		return "", err
	}
	if !first {
		return ResultDuplicate, nil
	}

	existing, err := p.subs.GetByProviderID(ctx, evt.Subscription.ProviderSubID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// First time we see this subscription: accept the provider snapshot.
		created := evt.Subscription
		created.Status = evt.TargetStatus()
		if err := p.subs.Upsert(ctx, created); err != nil {
			return "", err
		}
		p.cache.Bust(ctx, created.CustomerID)
		return ResultProcessed, nil
	case err != nil:
		return "", err
	}

	// Out-of-order guard: drop events older than our watermark.
	if evt.Subscription.ProviderUpdatedAt.Before(existing.ProviderUpdatedAt) {
		return ResultStale, nil
	}

	updated, err := domain.ApplyTransition(existing, evt.TargetStatus())
	if err != nil {
		// Illegal transition: never corrupt state. A real system alerts here.
		return ResultIllegal, nil
	}

	// Carry forward the provider's snapshot fields and advance the watermark.
	updated.PlanID = evt.Subscription.PlanID
	updated.SeatCount = evt.Subscription.SeatCount
	updated.CurrentPeriodEnd = evt.Subscription.CurrentPeriodEnd
	updated.CancelAtPeriodEnd = evt.Subscription.CancelAtPeriodEnd
	updated.ProviderUpdatedAt = evt.Subscription.ProviderUpdatedAt

	if err := p.subs.Upsert(ctx, updated); err != nil {
		return "", err
	}
	p.cache.Bust(ctx, updated.CustomerID)
	return ResultProcessed, nil
}
