// Package reconcile is the authoritative pull leg. Webhooks are best-effort
// push; a dropped or mis-ordered event leaves Postgres drifted from the
// provider. The reconciler periodically fetches provider truth, diffs it
// against local state, repairs drift, and busts the cache. It is what makes the
// system's answer trustworthy rather than "probably right".
package reconcile

import (
	"context"
	"errors"

	"subscriptioncore/cache"
	"subscriptioncore/domain"
	"subscriptioncore/provider"
	"subscriptioncore/store"
)

// Outcome reports what reconciling one subscription did.
type Outcome string

const (
	OutcomeInSync   Outcome = "in_sync"    // local already matched provider
	OutcomeRepaired Outcome = "repaired"   // drift found and corrected
	OutcomeAdopted  Outcome = "adopted"    // local row was missing; created from provider
	OutcomeStale    Outcome = "stale_skip" // provider snapshot older than local watermark
)

// Reconciler repairs drift between the provider and the source of truth.
type Reconciler struct {
	prov  provider.BillingProvider
	subs  store.SubscriptionRepo
	cache cache.EntitlementCache
}

// New wires a Reconciler.
func New(p provider.BillingProvider, subs store.SubscriptionRepo, c cache.EntitlementCache) *Reconciler {
	return &Reconciler{prov: p, subs: subs, cache: c}
}

// ReconcileOne fetches authoritative provider state for one subscription and
// repairs local state if it has drifted. It is safe to run at any time and is
// also the sync-on-read backstop for a missing local row.
func (r *Reconciler) ReconcileOne(ctx context.Context, providerSubID string) (Outcome, error) {
	truth, err := r.prov.FetchSubscription(providerSubID)
	if err != nil {
		return "", err
	}

	local, err := r.subs.GetByProviderID(ctx, providerSubID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		// A webhook was dropped entirely — adopt the provider snapshot.
		if err := r.subs.Upsert(ctx, truth); err != nil {
			return "", err
		}
		r.cache.Bust(ctx, truth.CustomerID)
		return OutcomeAdopted, nil
	case err != nil:
		return "", err
	}

	// Never let an older provider read overwrite a newer local watermark.
	if truth.ProviderUpdatedAt.Before(local.ProviderUpdatedAt) {
		return OutcomeStale, nil
	}

	if !drifted(local, truth) {
		return OutcomeInSync, nil
	}

	if err := r.subs.Upsert(ctx, truth); err != nil {
		return "", err
	}
	r.cache.Bust(ctx, truth.CustomerID)
	return OutcomeRepaired, nil
}

// ReconcileAll reconciles a set of provider subscription ids, returning the
// per-id outcomes. A caller (cron) supplies the working set — typically active
// or recently-changed subscriptions.
func (r *Reconciler) ReconcileAll(ctx context.Context, providerSubIDs []string) (map[string]Outcome, error) {
	out := make(map[string]Outcome, len(providerSubIDs))
	for _, id := range providerSubIDs {
		res, err := r.ReconcileOne(ctx, id)
		if err != nil {
			return out, err
		}
		out[id] = res
	}
	return out, nil
}

// drifted reports whether the billing-relevant fields differ. We compare only
// fields that affect entitlements/access, not bookkeeping timestamps.
func drifted(local, truth domain.Subscription) bool {
	return local.Status != truth.Status ||
		local.PlanID != truth.PlanID ||
		local.SeatCount != truth.SeatCount ||
		local.CancelAtPeriodEnd != truth.CancelAtPeriodEnd
}
