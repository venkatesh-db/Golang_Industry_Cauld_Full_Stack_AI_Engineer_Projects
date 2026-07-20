package tenant

import (
	"context"
	"errors"
	"sync"
)

// ErrNotFound is returned when a record does not exist for the current tenant.
// It is intentionally identical whether the record is absent or owned by another
// tenant, so existence is not leaked across tenants.
var ErrNotFound = errors.New("tenant: record not found")

// Record is a tenant-owned entity.
type Record struct {
	ID       string
	TenantID ID
	Data     string
}

// Store is an in-memory, tenant-scoped repository. Every read and write derives
// its tenant from the context — the caller cannot address another tenant's rows.
// A real implementation would apply the same scoping as a mandatory
// `WHERE tenant_id = $1` predicate on every SQL query.
type Store struct {
	mu   sync.RWMutex
	data map[string]Record // keyed by record ID
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{data: make(map[string]Record)}
}

// Put upserts a record owned by the context's tenant.
func (s *Store) Put(ctx context.Context, id, payload string) (Record, error) {
	tid, err := FromContext(ctx)
	if err != nil {
		return Record{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec := Record{ID: id, TenantID: tid, Data: payload}
	s.data[id] = rec
	return rec, nil
}

// Get returns a record only if it belongs to the context's tenant.
func (s *Store) Get(ctx context.Context, id string) (Record, error) {
	tid, err := FromContext(ctx)
	if err != nil {
		return Record{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.data[id]
	if !ok || rec.TenantID != tid {
		return Record{}, ErrNotFound
	}
	return rec, nil
}

// List returns every record owned by the context's tenant.
func (s *Store) List(ctx context.Context) ([]Record, error) {
	tid, err := FromContext(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, 0)
	for _, rec := range s.data {
		if rec.TenantID == tid {
			out = append(out, rec)
		}
	}
	return out, nil
}
