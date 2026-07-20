package search

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"railcache/internal/cache"
	"railcache/internal/metrics"
)

type sourceFunc func(context.Context, Query) (SearchResult, error)

func (f sourceFunc) Search(ctx context.Context, q Query) (SearchResult, error) { return f(ctx, q) }

type cacheEntry struct {
	value []byte
	ttl   time.Duration
}

type fakeStore struct {
	mu        sync.Mutex
	entries   map[string]cacheEntry
	getErr    error
	getHook   func(key string, call int) ([]byte, time.Duration, error)
	getCalls  int
	setCalls  int
	acquireOK bool
}

func (s *fakeStore) GetWithTTL(_ context.Context, key string) ([]byte, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	if s.getHook != nil {
		return s.getHook(key, s.getCalls)
	}
	if s.getErr != nil {
		return nil, 0, s.getErr
	}
	e, ok := s.entries[key]
	if !ok {
		return nil, 0, cache.ErrMiss
	}
	return e.value, e.ttl, nil
}

func (s *fakeStore) SetEx(_ context.Context, key string, value []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = make(map[string]cacheEntry)
	}
	s.setCalls++
	s.entries[key] = cacheEntry{value: value, ttl: ttl}
	return nil
}

func (s *fakeStore) Del(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
	return nil
}

func (s *fakeStore) Acquire(_ context.Context, _ string, _ time.Duration) (cache.Lease, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.acquireOK {
		return nil, false, nil
	}
	return fakeLease{}, true, nil
}

func (s *fakeStore) sets() int { s.mu.Lock(); defer s.mu.Unlock(); return s.setCalls }

type fakeLease struct{}

func (fakeLease) Release(context.Context) error { return nil }

func newTestService(src Source, store cache.Store, m *metrics.Metrics, p Params) *Service {
	return NewService(src, store, m, slog.New(slog.NewTextHandler(io.Discard, nil)), p)
}

func testParams() Params {
	return Params{
		TTL: 45 * time.Second, TatkalTTL: 4 * time.Second, NegativeTTL: 10 * time.Second,
		PhysicalMultiplier: 10, LockTTL: time.Second,
		WaitTries: 1, WaitEvery: time.Millisecond, FillTimeout: time.Second,
	}
}

func testResult(q Query) SearchResult {
	return SearchResult{Query: q, Trains: []Train{{Number: "12951", Name: "Mumbai Rajdhani", Class: q.Class, Available: 30, Total: 48, HasClass: true}}}
}

func envelope(t *testing.T, result SearchResult, freshFor time.Duration) []byte {
	t.Helper()
	b, err := json.Marshal(CacheEnvelope{Result: result, CachedAt: time.Now(), FreshUntil: time.Now().Add(freshFor)})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return b
}

func demoQuery() Query { return Query{From: "NDLS", To: "BCT", Date: "2026-07-20", Class: "3A"} }

func TestSearchServesFreshHitWithoutReadingSource(t *testing.T) {
	q := demoQuery()
	store := &fakeStore{entries: map[string]cacheEntry{
		CacheKey(q): {value: envelope(t, testResult(q), 30*time.Second)},
	}}
	var sourceCalls atomic.Int32
	svc := newTestService(sourceFunc(func(context.Context, Query) (SearchResult, error) {
		sourceCalls.Add(1)
		return SearchResult{}, nil
	}), store, metrics.New(), testParams())

	out, err := svc.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if out.Status != StatusHit || out.DBHit {
		t.Fatalf("outcome = %#v, want fresh HIT without DB access", out)
	}
	if sourceCalls.Load() != 0 {
		t.Fatalf("source calls = %d, want 0", sourceCalls.Load())
	}
}

func TestSearchServesStaleAndRevalidatesInBackground(t *testing.T) {
	q := demoQuery()
	store := &fakeStore{acquireOK: true, entries: map[string]cacheEntry{
		CacheKey(q): {value: envelope(t, testResult(q), -time.Second)}, // already logically stale
	}}
	m := metrics.New()
	var sourceCalls atomic.Int32
	svc := newTestService(sourceFunc(func(context.Context, Query) (SearchResult, error) {
		sourceCalls.Add(1)
		return testResult(q), nil
	}), store, m, testParams())

	out, err := svc.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if out.Status != StatusStale {
		t.Fatalf("status = %s, want STALE", out.Status)
	}
	// The background refresh must run exactly one revalidation query.
	waitFor(t, func() bool { return sourceCalls.Load() == 1 })
	svc.Drain(context.Background())
	if m.Refreshes.Load() != 1 {
		t.Fatalf("refreshes = %d, want 1", m.Refreshes.Load())
	}
}

func TestSearchMissFillsCache(t *testing.T) {
	q := demoQuery()
	store := &fakeStore{entries: make(map[string]cacheEntry), acquireOK: true}
	m := metrics.New()
	var sourceCalls atomic.Int32
	svc := newTestService(sourceFunc(func(_ context.Context, got Query) (SearchResult, error) {
		sourceCalls.Add(1)
		if got != q {
			t.Errorf("source query = %#v, want %#v", got, q)
		}
		return testResult(q), nil
	}), store, m, testParams())

	out, err := svc.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if out.Status != StatusMiss || !out.DBHit {
		t.Fatalf("outcome = %#v, want DB-backed MISS", out)
	}
	if sourceCalls.Load() != 1 || store.sets() != 1 || m.DBFills.Load() != 1 {
		t.Fatalf("source=%d sets=%d db_fills=%d, want 1 each", sourceCalls.Load(), store.sets(), m.DBFills.Load())
	}
}

func TestSearchFallsBackWhenCacheIsUnavailable(t *testing.T) {
	q := demoQuery()
	store := &fakeStore{getErr: errors.New("redis unavailable")}
	m := metrics.New()
	var sourceCalls atomic.Int32
	svc := newTestService(sourceFunc(func(context.Context, Query) (SearchResult, error) {
		sourceCalls.Add(1)
		return testResult(q), nil
	}), store, m, testParams())

	out, err := svc.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if out.Status != StatusFallback || !out.DBHit {
		t.Fatalf("outcome = %#v, want DB-backed FALLBACK", out)
	}
	if sourceCalls.Load() != 1 || m.Fallbacks.Load() != 1 {
		t.Fatalf("source=%d fallbacks=%d, want 1 each", sourceCalls.Load(), m.Fallbacks.Load())
	}
}

func TestSearchLockLoserRereadsWinnerCacheFill(t *testing.T) {
	q := demoQuery()
	value := envelope(t, testResult(q), 30*time.Second)
	store := &fakeStore{acquireOK: false}
	store.getHook = func(_ string, call int) ([]byte, time.Duration, error) {
		if call == 1 {
			return nil, 0, cache.ErrMiss
		}
		return value, 30 * time.Second, nil
	}
	var sourceCalls atomic.Int32
	svc := newTestService(sourceFunc(func(context.Context, Query) (SearchResult, error) {
		sourceCalls.Add(1)
		return SearchResult{}, nil
	}), store, metrics.New(), testParams())

	out, err := svc.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if out.Status != StatusHit || !out.Suppressed || out.DBHit {
		t.Fatalf("outcome = %#v, want suppressed HIT without DB access", out)
	}
	if sourceCalls.Load() != 0 {
		t.Fatalf("source calls = %d, want 0", sourceCalls.Load())
	}
}

func TestSearchBudgetExhaustionCoalescesDBFill(t *testing.T) {
	q := demoQuery()
	store := &fakeStore{entries: make(map[string]cacheEntry), acquireOK: false}
	m := metrics.New()
	var sourceCalls atomic.Int32
	p := testParams()
	p.WaitTries = 0 // go straight to the singleflight coalesce path
	svc := newTestService(sourceFunc(func(context.Context, Query) (SearchResult, error) {
		sourceCalls.Add(1)
		time.Sleep(25 * time.Millisecond)
		return testResult(q), nil
	}), store, m, p)

	start := make(chan struct{})
	errs := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := svc.Search(context.Background(), q); errs <- err }()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("Search() error = %v", err)
		}
	}
	if sourceCalls.Load() != 1 {
		t.Fatalf("source calls = %d, want one coalesced DB fill", sourceCalls.Load())
	}
	if m.DBFills.Load() != 1 {
		t.Fatalf("db fills = %d, want one actual DB fill", m.DBFills.Load())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
