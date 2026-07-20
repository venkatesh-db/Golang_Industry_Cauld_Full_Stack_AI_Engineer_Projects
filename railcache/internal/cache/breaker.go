package cache

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrBreakerOpen is returned by a Breaker while the circuit is open. It is a
// transport-class error (not ErrMiss), so the search service treats it exactly
// like "Redis is down" and falls back to Postgres — but in ~0ms, without a
// network round trip.
var ErrBreakerOpen = errors.New("cache: circuit breaker open")

// Breaker is a circuit-breaker decorator over a Store. It counts consecutive
// transport failures; once threshold is reached it opens for a cooldown window,
// short-circuiting every call so a Redis outage cannot make each request pay a
// dial/read timeout before falling back. After cooldown it half-opens and lets a
// single probe through: success closes it, failure re-opens it.
//
// ErrMiss is a normal outcome, not a failure, and never trips the breaker.
type Breaker struct {
	inner     Store
	threshold int
	cooldown  time.Duration
	now       func() time.Time // injectable for tests

	mu           sync.Mutex
	failures     int
	openUntil    time.Time
	halfOpen     bool
	stateChanged func(open bool) // optional observer (metrics)
}

// NewBreaker wraps inner. onState, if non-nil, is called whenever the circuit
// opens (true) or closes (false).
func NewBreaker(inner Store, threshold int, cooldown time.Duration, onState func(open bool)) *Breaker {
	return &Breaker{
		inner: inner, threshold: threshold, cooldown: cooldown,
		now: time.Now, stateChanged: onState,
	}
}

// allow decides whether a call may proceed and whether it is the half-open probe.
func (b *Breaker) allow() (ok bool, probe bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true, false // closed
	}
	if b.now().Before(b.openUntil) {
		return false, false // open
	}
	// Cooldown elapsed: allow exactly one probe through (half-open).
	if b.halfOpen {
		return false, false // a probe is already in flight
	}
	b.halfOpen = true
	return true, true
}

// record folds a call's outcome into breaker state.
func (b *Breaker) record(err error, probe bool) {
	// A miss (or nil) is a success; only transport errors count against us.
	success := err == nil || errors.Is(err, ErrMiss)

	b.mu.Lock()
	defer b.mu.Unlock()
	if success {
		wasOpen := !b.openUntil.IsZero()
		b.failures = 0
		b.openUntil = time.Time{}
		b.halfOpen = false
		if wasOpen && b.stateChanged != nil {
			b.stateChanged(false)
		}
		return
	}
	if probe {
		// Probe failed: stay open for another cooldown.
		b.openUntil = b.now().Add(b.cooldown)
		b.halfOpen = false
		return
	}
	b.failures++
	if b.failures >= b.threshold && b.openUntil.IsZero() {
		b.openUntil = b.now().Add(b.cooldown)
		if b.stateChanged != nil {
			b.stateChanged(true)
		}
	}
}

func (b *Breaker) GetWithTTL(ctx context.Context, key string) ([]byte, time.Duration, error) {
	ok, probe := b.allow()
	if !ok {
		return nil, 0, ErrBreakerOpen
	}
	v, ttl, err := b.inner.GetWithTTL(ctx, key)
	b.record(err, probe)
	return v, ttl, err
}

func (b *Breaker) SetEx(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	ok, probe := b.allow()
	if !ok {
		return ErrBreakerOpen
	}
	err := b.inner.SetEx(ctx, key, val, ttl)
	b.record(err, probe)
	return err
}

func (b *Breaker) Del(ctx context.Context, key string) error {
	ok, probe := b.allow()
	if !ok {
		return ErrBreakerOpen
	}
	err := b.inner.Del(ctx, key)
	b.record(err, probe)
	return err
}

func (b *Breaker) Acquire(ctx context.Context, key string, ttl time.Duration) (Lease, bool, error) {
	ok, probe := b.allow()
	if !ok {
		return nil, false, ErrBreakerOpen
	}
	lease, acquired, err := b.inner.Acquire(ctx, key, ttl)
	b.record(err, probe)
	return lease, acquired, err
}
