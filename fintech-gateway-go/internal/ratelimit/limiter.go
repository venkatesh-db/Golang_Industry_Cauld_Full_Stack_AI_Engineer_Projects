// Package ratelimit protects backends from abuse (brute-forced OTPs,
// scripted payment retries, scraping) with a per-key token bucket.
//
// A single mutex guarding one shared map is the first thing that falls
// over under high concurrency: every request across every key serializes
// on the same lock. Limiter instead shards keys across N independent
// buckets-maps, each with its own mutex, so unrelated keys (different
// API callers, different account IDs) essentially never contend.
package ratelimit

import (
	"hash/fnv"
	"sync"
	"time"
)

const defaultShardCount = 256

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

type shard struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

// Limiter is a sharded token-bucket rate limiter. Rate is tokens added
// per second; Burst is the bucket capacity. Safe for concurrent use.
//
// A background sweeper evicts buckets idle for longer than idleTTL. This
// is what actually bounds memory for a public gateway: a caller who
// makes one request and never returns leaves a bucket that Allow itself
// will never touch again, so eviction on access alone can't reclaim it —
// only a periodic sweep across all shards can. Call Close to stop the
// sweeper when the Limiter is no longer needed.
type Limiter struct {
	rate    float64
	burst   float64
	idleTTL time.Duration
	shards  []*shard
	now     func() time.Time

	stop chan struct{}
	done chan struct{}
}

func New(ratePerSecond float64, burst int) *Limiter {
	return newWithClock(ratePerSecond, burst, time.Now)
}

// NewWithClock is New with an injectable time source. It exists so callers
// (and tests in other packages) can freeze time and assert token-bucket
// behavior deterministically, instead of racing the wall clock — a
// rate-limit test that depends on real elapsed time is inherently flaky.
func NewWithClock(ratePerSecond float64, burst int, now func() time.Time) *Limiter {
	return newWithClock(ratePerSecond, burst, now)
}

func newWithClock(ratePerSecond float64, burst int, now func() time.Time) *Limiter {
	shards := make([]*shard, defaultShardCount)
	for i := range shards {
		shards[i] = &shard{buckets: make(map[string]*bucket)}
	}
	// A bucket refills fully within burst/rate seconds of inactivity;
	// give it 4x that as a safety margin before treating it as idle.
	idleTTL := time.Duration(float64(burst)/ratePerSecond*4) * time.Second
	if idleTTL < time.Minute {
		idleTTL = time.Minute
	}
	l := &Limiter{
		rate: ratePerSecond, burst: float64(burst), idleTTL: idleTTL,
		shards: shards, now: now,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go l.sweepLoop()
	return l
}

func (l *Limiter) sweepLoop() {
	defer close(l.done)
	interval := l.idleTTL / 4
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.sweep()
		case <-l.stop:
			return
		}
	}
}

func (l *Limiter) sweep() {
	cutoff := l.now().Add(-l.idleTTL)
	for _, s := range l.shards {
		s.mu.Lock()
		for key, b := range s.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(s.buckets, key)
			}
		}
		s.mu.Unlock()
	}
}

// Close stops the background sweeper. Every Limiter created with New
// must eventually have Close called, or its sweeper goroutine leaks.
func (l *Limiter) Close() {
	close(l.stop)
	<-l.done
}

func (l *Limiter) shardFor(key string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return l.shards[h.Sum32()%uint32(len(l.shards))]
}

// Allow reports whether one request for key is permitted right now, and
// consumes a token if so. O(1) and touches only the one shard owning key.
func (l *Limiter) Allow(key string) bool {
	s := l.shardFor(key)
	now := l.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst - 1, lastSeen: now}
		s.buckets[key] = b
		return true
	}

	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens += elapsed * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
