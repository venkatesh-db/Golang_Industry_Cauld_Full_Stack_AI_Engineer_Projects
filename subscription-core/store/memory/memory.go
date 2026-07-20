// Package memory is an in-memory implementation of the store ports for tests
// and local runs. It satisfies SubscriptionRepo, WebhookEventRepo and PlanRepo.
package memory

import (
	"context"
	"sync"

	"subscriptioncore/domain"
	"subscriptioncore/store"
)

// Store is a concurrency-safe in-memory persistence layer.
type Store struct {
	mu     sync.RWMutex
	byProv map[string]domain.Subscription
	bySubj map[string]domain.Subscription
	events map[string]bool
	plans  map[string]domain.Plan
}

// New returns an empty Store.
func New() *Store {
	return &Store{
		byProv: map[string]domain.Subscription{},
		bySubj: map[string]domain.Subscription{},
		events: map[string]bool{},
		plans:  map[string]domain.Plan{},
	}
}

// SeedPlan registers a plan (test/setup helper).
func (s *Store) SeedPlan(p domain.Plan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[p.ID] = p
}

func (s *Store) GetByProviderID(_ context.Context, id string) (domain.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sub, ok := s.byProv[id]
	if !ok {
		return domain.Subscription{}, store.ErrNotFound
	}
	return sub, nil
}

func (s *Store) GetBySubject(_ context.Context, subject string) (domain.Subscription, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sub, ok := s.bySubj[subject]
	if !ok {
		return domain.Subscription{}, store.ErrNotFound
	}
	return sub, nil
}

func (s *Store) Upsert(_ context.Context, sub domain.Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byProv[sub.ProviderSubID] = sub
	s.bySubj[sub.CustomerID] = sub
	return nil
}

func (s *Store) MarkProcessed(_ context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.events[id] {
		return false, nil
	}
	s.events[id] = true
	return true, nil
}

func (s *Store) Get(_ context.Context, planID string) (domain.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plans[planID]
	if !ok {
		return domain.Plan{}, store.ErrNotFound
	}
	return p, nil
}
