// Package entitlements is the request-path service: cache-aside reads that fail
// open to Postgres, with metered-usage-aware decisions.
package entitlements

import (
	"context"

	"subscriptioncore/cache"
	"subscriptioncore/domain"
	"subscriptioncore/store"
	"subscriptioncore/usage"
)

// Service answers "is this subject allowed to use this feature right now?".
type Service struct {
	subs  store.SubscriptionRepo
	plans store.PlanRepo
	cache cache.EntitlementCache
	meter *usage.Meter
}

// New wires the entitlements service.
func New(subs store.SubscriptionRepo, plans store.PlanRepo, c cache.EntitlementCache, m *usage.Meter) *Service {
	return &Service{subs: subs, plans: plans, cache: c, meter: m}
}

// Entitlement returns a subject's entitlement projection. Cache-aside: try the
// cache, on miss derive from the source of truth and repopulate. If the cache
// is unavailable the derive path still answers — fail open, never deny a paying
// customer because Redis is down.
func (s *Service) Entitlement(ctx context.Context, subjectID string) (domain.Entitlement, error) {
	if e, ok := s.cache.Get(ctx, subjectID); ok {
		return e, nil
	}
	e, err := s.derive(ctx, subjectID)
	if err != nil {
		return domain.Entitlement{}, err
	}
	s.cache.Set(ctx, subjectID, e) // no-op when the cache is down
	return e, nil
}

func (s *Service) derive(ctx context.Context, subjectID string) (domain.Entitlement, error) {
	sub, err := s.subs.GetBySubject(ctx, subjectID)
	if err != nil {
		return domain.Entitlement{}, err
	}
	plan, err := s.plans.Get(ctx, sub.PlanID)
	if err != nil {
		return domain.Entitlement{}, err
	}
	return domain.DeriveEntitlement(sub, plan), nil
}

// Check returns an entitlement decision, layering metered usage on top of the
// base plan decision. Unlimited features (-1) skip the usage read entirely.
func (s *Service) Check(ctx context.Context, subjectID string, feature domain.Feature) (domain.Decision, error) {
	e, err := s.Entitlement(ctx, subjectID)
	if err != nil {
		return domain.Decision{}, err
	}
	d := e.Evaluate(feature)
	if !d.Allow || d.Limit < 0 {
		return d, nil
	}
	used, err := s.meter.Used(ctx, subjectID, string(feature))
	if err != nil {
		return domain.Decision{}, err
	}
	if used >= d.Limit {
		return domain.Decision{Allow: false, Reason: domain.ReasonQuotaExceeded, Limit: d.Limit}, nil
	}
	return d, nil
}
