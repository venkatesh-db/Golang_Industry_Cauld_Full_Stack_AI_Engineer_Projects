// Package loadbalancer distributes requests across a fixed set of
// backend instances (e.g. replicas of a payments service) using one of
// several strategies, skipping backends a health checker has marked
// unhealthy.
package loadbalancer

import "sync/atomic"

// Backend is one instance behind the pool. Health and connection count
// are read on every Pick call, so they're atomics rather than
// mutex-guarded fields.
type Backend struct {
	ID      string
	Address string
	Weight  int

	healthy     atomic.Bool
	activeConns atomic.Int64
}

func NewBackend(id, address string, weight int) *Backend {
	if weight < 1 {
		weight = 1
	}
	b := &Backend{ID: id, Address: address, Weight: weight}
	b.healthy.Store(true)
	return b
}

func (b *Backend) SetHealthy(v bool)        { b.healthy.Store(v) }
func (b *Backend) Healthy() bool            { return b.healthy.Load() }
func (b *Backend) ActiveConnections() int64 { return b.activeConns.Load() }
