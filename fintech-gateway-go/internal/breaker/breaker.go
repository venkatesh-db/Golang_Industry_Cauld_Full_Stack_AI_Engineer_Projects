// Package breaker implements a per-backend circuit breaker: once a
// backend's failure rate crosses a threshold, the breaker trips open and
// fails requests immediately instead of forwarding them — cheap local
// failures instead of piling up slow, doomed calls (connection pool
// exhaustion, goroutines blocked waiting on a backend that's already
// down) that would otherwise cascade into every other backend.
package breaker

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var ErrOpen = errors.New("breaker: circuit is open")

type state int32

const (
	closed state = iota
	open
	halfOpen
)

// Config controls when the breaker trips and how it recovers.
type Config struct {
	// FailureThreshold: consecutive failures in Closed state that trip
	// the breaker to Open.
	FailureThreshold int
	// OpenDuration: how long the breaker stays Open before allowing
	// trial requests through as HalfOpen.
	OpenDuration time.Duration
	// HalfOpenSuccessThreshold: consecutive successes in HalfOpen needed
	// to close the breaker again.
	HalfOpenSuccessThreshold int
	// MaxHalfOpenRequests bounds how many trial requests are let through
	// per HalfOpen period, so a still-broken backend isn't immediately
	// re-flooded. Must be >= HalfOpenSuccessThreshold, or the breaker
	// could never observe enough successes to close and would sit in
	// HalfOpen (neither serving normally nor failing fast) until the
	// next failure reopens it.
	MaxHalfOpenRequests int
}

func DefaultConfig() Config {
	return Config{
		FailureThreshold:         5,
		OpenDuration:             10 * time.Second,
		HalfOpenSuccessThreshold: 2,
		MaxHalfOpenRequests:      5,
	}
}

// Breaker is safe for concurrent use. The hot path (Allow, on the
// Closed/Open fast paths) only touches atomics — no lock — since it's
// called on every single request.
type Breaker struct {
	cfg Config
	now func() time.Time

	state           atomic.Int32
	consecutiveFail atomic.Int32
	consecutiveOK   atomic.Int32
	halfOpenIssued  atomic.Int32
	openedAt        atomic.Int64 // unix nanos

	// mu guards state transitions (Open→HalfOpen, HalfOpen→Closed,
	// →Open) so they happen exactly once even under concurrent callers.
	mu sync.Mutex
}

func New(cfg Config) *Breaker {
	return newWithClock(cfg, time.Now)
}

func newWithClock(cfg Config, now func() time.Time) *Breaker {
	if cfg.MaxHalfOpenRequests < cfg.HalfOpenSuccessThreshold {
		cfg.MaxHalfOpenRequests = cfg.HalfOpenSuccessThreshold
	}
	return &Breaker{cfg: cfg, now: now}
}

// Allow reports whether a request may proceed. Callers that get true
// must report the outcome via Success or Failure.
func (b *Breaker) Allow() bool {
	switch state(b.state.Load()) {
	case closed:
		return true
	case halfOpen:
		return b.halfOpenIssued.Add(1) <= int32(b.cfg.MaxHalfOpenRequests)
	case open:
		if b.now().UnixNano() < b.openedAt.Load()+int64(b.cfg.OpenDuration) {
			return false
		}
		return b.tryTransitionToHalfOpen()
	default:
		return false
	}
}

func (b *Breaker) tryTransitionToHalfOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if state(b.state.Load()) != open {
		return false
	}
	b.consecutiveOK.Store(0)
	b.halfOpenIssued.Store(1) // this call is itself the first trial request
	b.state.Store(int32(halfOpen))
	return true
}

func (b *Breaker) Success() {
	switch state(b.state.Load()) {
	case closed:
		b.consecutiveFail.Store(0)
	case halfOpen:
		if b.consecutiveOK.Add(1) >= int32(b.cfg.HalfOpenSuccessThreshold) {
			b.mu.Lock()
			if state(b.state.Load()) == halfOpen {
				b.state.Store(int32(closed))
				b.consecutiveFail.Store(0)
			}
			b.mu.Unlock()
		}
	}
}

func (b *Breaker) Failure() {
	switch state(b.state.Load()) {
	case closed:
		if b.consecutiveFail.Add(1) >= int32(b.cfg.FailureThreshold) {
			b.trip()
		}
	case halfOpen:
		b.trip() // a single failed trial reopens immediately
	}
}

func (b *Breaker) trip() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.state.Store(int32(open))
	b.openedAt.Store(b.now().UnixNano())
	b.consecutiveFail.Store(0)
}
