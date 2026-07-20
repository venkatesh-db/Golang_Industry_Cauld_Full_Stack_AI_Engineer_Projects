// Package idempotency provides the key store payment.Debit uses to make
// client retries safe.
package idempotency

import (
	"context"
	"errors"
	"sync"
)

var ErrInProgress = errors.New("idempotency: a request with this key is already in progress")

type Record struct {
	Key  string
	Body []byte
}

// Store is the interface production code depends on. In production this
// is backed by Postgres (`INSERT ... ON CONFLICT (idempotency_key) DO
// NOTHING`, a unique constraint enforced by the database, not a
// best-effort in-process check) or Redis SETNX with a TTL — see
// docs/DESIGN.md, "distributed lock instead of DB-enforced correctness",
// for why a bare Redis lock is not sufficient here.
type Store interface {
	// Reserve atomically claims key. If the key already completed, it
	// returns the stored record with found=true. If a request with the
	// same key is currently in flight, it returns ErrInProgress. If the
	// caller now owns the key, it returns (nil, false, nil) and the
	// caller must call Complete or Release.
	Reserve(ctx context.Context, key string) (rec *Record, found bool, err error)
	Complete(ctx context.Context, key string, rec Record) error
	Release(ctx context.Context, key string) error
}

// MemStore is an in-memory Store for tests and local dev.
type MemStore struct {
	mu      sync.Mutex
	done    map[string]Record
	pending map[string]struct{}
}

func NewMemStore() *MemStore {
	return &MemStore{done: make(map[string]Record), pending: make(map[string]struct{})}
}

func (s *MemStore) Reserve(ctx context.Context, key string) (*Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.done[key]; ok {
		return &rec, true, nil
	}
	if _, ok := s.pending[key]; ok {
		return nil, false, ErrInProgress
	}
	s.pending[key] = struct{}{}
	return nil, false, nil
}

func (s *MemStore) Complete(ctx context.Context, key string, rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, key)
	s.done[key] = rec
	return nil
}

func (s *MemStore) Release(ctx context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, key)
	return nil
}
