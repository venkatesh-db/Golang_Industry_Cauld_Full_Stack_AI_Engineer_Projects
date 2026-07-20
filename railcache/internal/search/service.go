package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"railcache/internal/cache"
	"railcache/internal/metrics"
)

// Source is the source of truth the cache shields (Postgres in production).
type Source interface {
	Search(ctx context.Context, q Query) (SearchResult, error)
}

// Params are the freshness/lock tunables the read path needs.
type Params struct {
	TTL                time.Duration // base logical TTL for positive results
	TatkalTTL          time.Duration // logical TTL for near-date, high-churn queries
	Jitter             float64       // +/- fraction applied to logical TTL
	NegativeTTL        time.Duration // logical TTL for empty results
	PhysicalMultiplier int           // physical Redis TTL = logical * this
	LockTTL            time.Duration
	WaitTries          int
	WaitEvery          time.Duration
	FillTimeout        time.Duration // deadline for a detached DB fill/refresh
}

// Service implements the cache-aside read path with stale-while-revalidate
// freshness (logical expiry in the envelope), distributed-lock stampede
// protection for cold fills, an in-process per-key refresh gate, negative
// caching, and graceful Postgres fallback when Redis is unavailable.
type Service struct {
	src Source
	cc  cache.Store
	m   *metrics.Metrics
	log *slog.Logger
	p   Params

	// fillSF coalesces concurrent cold-fill fallbacks for the same key on this
	// instance so a slow lock owner can't let every waiting loser hit Postgres.
	fillSF singleflight.Group
	// refreshing gates background refreshes to at most one goroutine per key per
	// instance — without it, every request in the stale window would spawn a
	// goroutine and a losing Redis lock attempt (a QPS-scaled amplifier).
	refreshing sync.Map
	refreshWG  sync.WaitGroup
}

// NewService wires the read path.
func NewService(src Source, cc cache.Store, m *metrics.Metrics, log *slog.Logger, p Params) *Service {
	return &Service{src: src, cc: cc, m: m, log: log, p: p}
}

// Search serves one request. The query is expected to be validated by the
// caller; Normalize is idempotent and applied again defensively.
func (s *Service) Search(ctx context.Context, q Query) (Outcome, error) {
	q = q.Normalize()
	key := CacheKey(q)

	val, _, err := s.cc.GetWithTTL(ctx, key)
	switch {
	case err == nil:
		env, derr := decode(val)
		if derr != nil {
			s.m.Errors.Add(1)
			s.log.Warn("corrupt cache envelope, refilling", "key", key, "err", derr)
			return s.fill(ctx, q, key)
		}
		if env.stale(time.Now()) {
			// Serve stale immediately and revalidate in the background. The lock
			// mechanism is now a cold-start-only concern; hot keys never hard
			// expire under load because physical TTL far exceeds logical.
			s.triggerRefresh(key, q)
			s.m.Stale.Add(1)
			return Outcome{Result: env.Result, Status: StatusStale, AsOf: env.CachedAt}, nil
		}
		s.m.Hits.Add(1)
		return Outcome{Result: env.Result, Status: StatusHit, AsOf: env.CachedAt}, nil
	case errors.Is(err, cache.ErrMiss):
		return s.fill(ctx, q, key)
	default:
		// Transport error or open circuit → degrade to Postgres, never 5xx.
		return s.fallback(ctx, q)
	}
}

// fill is the cold-miss path: one lock winner queries the DB; losers wait then
// coalesce. Rare in steady state thanks to stale-while-revalidate.
func (s *Service) fill(ctx context.Context, q Query, key string) (Outcome, error) {
	lock, ok, err := s.cc.Acquire(ctx, LockKey(key), s.p.LockTTL)
	if err != nil {
		return s.fallback(ctx, q) // lock transport error == Redis trouble
	}
	if ok {
		defer s.releaseLock(lock, key)
		res, took, asOf, qerr := s.fillAndStore(ctx, q, key)
		if qerr != nil {
			s.m.Errors.Add(1)
			return Outcome{}, fmt.Errorf("fill %q: %w", key, qerr)
		}
		s.m.Misses.Add(1)
		return Outcome{Result: res, Status: StatusMiss, DBHit: true, DBTook: took, AsOf: asOf}, nil
	}

	// Loser: another filler holds the lock. Re-read the cache a few times,
	// honoring context cancellation so a dead request doesn't sleep its budget.
	for i := 0; i < s.p.WaitTries; i++ {
		select {
		case <-ctx.Done():
			return Outcome{}, fmt.Errorf("wait for fill %q: %w", key, ctx.Err())
		case <-time.After(s.p.WaitEvery):
		}
		val, _, gerr := s.cc.GetWithTTL(ctx, key)
		if gerr == nil {
			if env, derr := decode(val); derr == nil {
				s.m.HerdSuppressed.Add(1)
				return Outcome{Result: env.Result, Status: StatusHit, Suppressed: true, AsOf: env.CachedAt}, nil
			}
		} else if !errors.Is(gerr, cache.ErrMiss) {
			return s.fallback(ctx, q) // Redis died mid-wait
		}
	}

	// Budget exhausted: the owner is slow or crashed. Collapse concurrent
	// losers on this instance into one query via singleflight.
	v, err, shared := s.fillSF.Do(key, func() (any, error) {
		res, took, asOf, qerr := s.fillAndStore(ctx, q, key)
		if qerr != nil {
			return nil, qerr
		}
		return fillOutput{res: res, took: took, asOf: asOf}, nil
	})
	if err != nil {
		s.m.Errors.Add(1)
		return Outcome{}, fmt.Errorf("budget-exhausted fill %q: %w", key, err)
	}
	out := v.(fillOutput)
	if shared {
		// Coalesced with peers: this request avoided its own DB query.
		s.m.HerdSuppressed.Add(1)
		return Outcome{Result: out.res, Status: StatusHit, Suppressed: true, DBHit: false, AsOf: out.asOf}, nil
	}
	s.m.Misses.Add(1)
	return Outcome{Result: out.res, Status: StatusMiss, DBHit: true, DBTook: out.took, AsOf: out.asOf}, nil
}

// fillOutput is the value shared across singleflight callers.
type fillOutput struct {
	res  SearchResult
	took time.Duration
	asOf time.Time
}

// fallback serves directly from Postgres when Redis is unavailable/open.
func (s *Service) fallback(ctx context.Context, q Query) (Outcome, error) {
	res, took, err := s.queryDB(ctx, q)
	if err != nil {
		s.m.Errors.Add(1)
		return Outcome{}, fmt.Errorf("fallback search: %w", err)
	}
	s.m.Fallbacks.Add(1)
	return Outcome{Result: res, Status: StatusFallback, DBHit: true, DBTook: took, AsOf: time.Now()}, nil
}

// triggerRefresh schedules at most one background revalidation per key per
// instance. Concurrent callers in the stale window return immediately here.
func (s *Service) triggerRefresh(key string, q Query) {
	if _, busy := s.refreshing.LoadOrStore(key, struct{}{}); busy {
		return
	}
	s.refreshWG.Add(1)
	go func() {
		defer s.refreshWG.Done()
		defer s.refreshing.Delete(key)
		s.doRefresh(key, q)
	}()
}

// doRefresh revalidates a stale key. The distributed lock deduplicates refreshes
// across instances; a DB error keeps the existing stale value (stale-if-error).
func (s *Service) doRefresh(key string, q Query) {
	ctx, cancel := context.WithTimeout(context.Background(), s.p.FillTimeout)
	defer cancel()
	lock, ok, err := s.cc.Acquire(ctx, LockKey(key), s.p.LockTTL)
	if err != nil || !ok {
		return // another refresher/instance owns it
	}
	defer s.releaseLock(lock, key)
	res, _, qerr := s.queryDB(ctx, q)
	if qerr != nil {
		s.m.Errors.Add(1)
		s.log.Warn("refresh failed; serving stale until physical expiry", "key", key, "err", qerr)
		return
	}
	s.store(ctx, key, res)
	s.m.Refreshes.Add(1)
}

// Invalidate busts a cached key (write-path demo / booking-event hook).
func (s *Service) Invalidate(ctx context.Context, q Query) error {
	return s.cc.Del(ctx, CacheKey(q.Normalize()))
}

// Drain waits for in-flight background refreshes to finish, or until ctx is done.
func (s *Service) Drain(ctx context.Context) {
	done := make(chan struct{})
	go func() { s.refreshWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// fillAndStore runs the DB query and writes the cache on a detached, bounded
// context so a caller disconnecting cannot cancel work that other waiters (and
// the cache itself) depend on.
func (s *Service) fillAndStore(ctx context.Context, q Query, key string) (SearchResult, time.Duration, time.Time, error) {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.p.FillTimeout)
	defer cancel()
	res, took, err := s.queryDB(fctx, q)
	if err != nil {
		return SearchResult{}, took, time.Time{}, err
	}
	asOf := time.Now()
	s.store(fctx, key, res)
	s.m.DBFills.Add(1)
	return res, took, asOf, nil
}

// releaseLock releases a fill/refresh lock, logging rather than swallowing a
// failure so operators can see when a lock outlives its intended holder.
func (s *Service) releaseLock(lock cache.Lease, key string) {
	if err := lock.Release(context.Background()); err != nil {
		s.log.Warn("lock release failed", "key", key, "err", err)
	}
}

// queryDB times a single source-of-truth read.
func (s *Service) queryDB(ctx context.Context, q Query) (SearchResult, time.Duration, error) {
	start := time.Now()
	res, err := s.src.Search(ctx, q)
	return res, time.Since(start), err
}

// store writes an envelope with logical expiry (FreshUntil) and a physical TTL
// several times longer, so the value can be served stale while it revalidates.
// Best-effort: a write failure is logged, not fatal.
func (s *Service) store(ctx context.Context, key string, res SearchResult) {
	now := time.Now()
	logical := s.p.NegativeTTL
	if len(res.Trains) > 0 {
		logical = s.p.freshnessFor(res.Query)
	}
	logical = jitterTTL(logical, s.p.Jitter)

	env := CacheEnvelope{Result: res, Empty: len(res.Trains) == 0, CachedAt: now, FreshUntil: now.Add(logical)}
	blob, err := json.Marshal(env)
	if err != nil {
		s.m.Errors.Add(1)
		s.log.Error("marshal envelope", "key", key, "err", err)
		return
	}
	physical := logical * time.Duration(s.p.PhysicalMultiplier)
	if err := s.cc.SetEx(ctx, key, blob, physical); err != nil {
		s.m.Errors.Add(1)
		s.log.Warn("cache write failed", "key", key, "err", err)
	}
}

func decode(val []byte) (CacheEnvelope, error) {
	var env CacheEnvelope
	err := json.Unmarshal(val, &env)
	return env, err
}
