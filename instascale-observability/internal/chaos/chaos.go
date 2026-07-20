// Package chaos is the failure laboratory. Every injector is opt-in behind
// CHAOS_ENABLED, isolated, and reversible via Reset. Handlers/stores consult the
// shared state (SlowDepMS, Fail503) so a mode produces a distinct telemetry
// signature without touching the normal code path when disabled.
package chaos

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// PoolExhauster is implemented by a store that can hold/release N DB connections,
// letting the db-pool-exhaust mode drive pgxpool acquire-waits.
type PoolExhauster interface {
	HoldConns(ctx context.Context, n int) // acquire n conns and hold them
	ReleaseConns()                        // release everything held
}

// Registry owns all chaos state for one service.
type Registry struct {
	enabled bool

	slowDepMS atomic.Int64 // injected per-request latency in ms (slow-dependency)
	fail503   atomic.Bool  // force 500s so upstream retries amplify (retry-storm)

	mu       sync.Mutex
	leakStop []chan struct{} // goroutine-leak: blocked goroutines to release on reset
	memHold  [][]byte        // memory-pressure: retained allocations

	pool PoolExhauster
}

func New(enabled bool) *Registry { return &Registry{enabled: enabled} }

// Enabled reports whether chaos routes should be registered at all.
func (r *Registry) Enabled() bool { return r.enabled }

// AttachPool lets the db-pool-exhaust mode reach the service's connection pool.
func (r *Registry) AttachPool(p PoolExhauster) { r.pool = p }

// --- consulted by the normal request path (cheap when idle) ---

// SlowDepDelay returns the currently injected latency (0 when off).
func (r *Registry) SlowDepDelay() time.Duration {
	return time.Duration(r.slowDepMS.Load()) * time.Millisecond
}

// ShouldFail reports whether handlers should return 500 (retry-storm driver).
func (r *Registry) ShouldFail() bool { return r.fail503.Load() }

// --- injectors ---

// GoroutineLeak spawns n goroutines blocked forever on an unbuffered channel.
// Signature: the `goroutines` gauge ramps monotonically and RSS climbs.
func (r *Registry) GoroutineLeak(n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := 0; i < n; i++ {
		stop := make(chan struct{})
		block := make(chan struct{})
		r.leakStop = append(r.leakStop, stop)
		go func() {
			select {
			case <-stop:
			case <-block: // never sent -> the goroutine leaks until reset
			}
		}()
	}
}

// MemPressure retains mb megabytes of live, touched memory.
// Signature: go_memstats_alloc_bytes + RSS climb, GC pause up.
func (r *Registry) MemPressure(mb int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := 0; i < mb; i++ {
		b := make([]byte, 1<<20)
		for j := range b { // touch pages so they're actually resident
			b[j] = byte(j)
		}
		r.memHold = append(r.memHold, b)
	}
}

// SlowDep injects ms of latency into every request until reset.
// Signature: p95/p99 latency alert fires; the slow span shows in the trace.
func (r *Registry) SlowDep(ms int) { r.slowDepMS.Store(int64(ms)) }

// RetryStorm forces handlers to return 500 so bounded upstream retries amplify,
// then the edge-api breaker opens. Signature: downstream rate multiplies, error
// spike, breaker trips.
func (r *Registry) RetryStorm(on bool) { r.fail503.Store(on) }

// DBPoolExhaust holds n DB connections without releasing them.
// Signature: db_pool_acquired == max, acquire-wait latency spikes, readyz degrades.
func (r *Registry) DBPoolExhaust(ctx context.Context, n int) {
	if r.pool != nil {
		r.pool.HoldConns(ctx, n)
	}
}

// Reset reverses every active injector, returning the service to health.
func (r *Registry) Reset() {
	r.slowDepMS.Store(0)
	r.fail503.Store(false)

	r.mu.Lock()
	for _, stop := range r.leakStop {
		close(stop)
	}
	r.leakStop = nil
	r.memHold = nil // drop references; next GC reclaims
	r.mu.Unlock()

	if r.pool != nil {
		r.pool.ReleaseConns()
	}
}
