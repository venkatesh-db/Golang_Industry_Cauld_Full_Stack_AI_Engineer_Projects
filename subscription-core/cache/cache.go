// Package cache defines the hot entitlement read path (Redis in production) and
// an in-memory implementation that can simulate an outage.
package cache

import (
	"context"
	"sync"

	"subscriptioncore/domain"
)

// EntitlementCache is a rebuildable projection cache. It must never be treated
// as the source of truth: a miss or outage falls back to Postgres.
type EntitlementCache interface {
	Get(ctx context.Context, subjectID string) (domain.Entitlement, bool)
	Set(ctx context.Context, subjectID string, e domain.Entitlement)
	Bust(ctx context.Context, subjectID string)
}

// Memory is an in-memory EntitlementCache. SetDown(true) simulates a Redis
// outage so fail-open behavior can be tested.
type Memory struct {
	mu   sync.RWMutex
	data map[string]domain.Entitlement
	down bool
}

// NewMemory returns an empty cache.
func NewMemory() *Memory {
	return &Memory{data: map[string]domain.Entitlement{}}
}

// SetDown toggles simulated unavailability.
func (m *Memory) SetDown(down bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.down = down
}

func (m *Memory) Get(_ context.Context, subject string) (domain.Entitlement, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.down {
		return domain.Entitlement{}, false
	}
	e, ok := m.data[subject]
	return e, ok
}

func (m *Memory) Set(_ context.Context, subject string, e domain.Entitlement) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.down {
		return
	}
	m.data[subject] = e
}

func (m *Memory) Bust(_ context.Context, subject string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, subject)
}
