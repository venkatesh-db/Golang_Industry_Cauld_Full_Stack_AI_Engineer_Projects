package loadbalancer

import (
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"
)

var ErrNoHealthyBackends = errors.New("loadbalancer: no healthy backends available")

type Strategy int

const (
	RoundRobin Strategy = iota
	LeastConnections
	WeightedRandom
	ConsistentHash
)

// Pool selects a backend from a fixed set using one strategy. The
// backend set is immutable after NewPool — adding/removing backends
// (a deploy, an autoscale event) means building a new Pool and swapping
// it in, not mutating this one, so Pick never needs to lock against
// concurrent membership changes.
type Pool struct {
	strategy  Strategy
	backends  []*Backend
	rrCounter atomic.Uint64
	ring      *hashRing
}

func NewPool(strategy Strategy, backends []*Backend) (*Pool, error) {
	if len(backends) == 0 {
		return nil, errors.New("loadbalancer: pool requires at least one backend")
	}
	p := &Pool{strategy: strategy, backends: backends}
	if strategy == ConsistentHash {
		p.ring = newHashRing(backends)
	}
	return p, nil
}

// Pick selects a backend and marks it as having one more active
// connection. Callers must call Release when the request completes.
// key is only used by the ConsistentHash strategy; other strategies
// ignore it.
func (p *Pool) Pick(key string) (*Backend, error) {
	var b *Backend
	switch p.strategy {
	case RoundRobin:
		b = p.pickRoundRobin()
	case LeastConnections:
		b = p.pickLeastConnections()
	case WeightedRandom:
		b = p.pickWeightedRandom()
	case ConsistentHash:
		b = p.ring.get(key)
	default:
		return nil, fmt.Errorf("loadbalancer: unknown strategy %d", p.strategy)
	}
	if b == nil {
		return nil, ErrNoHealthyBackends
	}
	b.activeConns.Add(1)
	return b, nil
}

// Release must be called exactly once for every successful Pick, when
// the request against that backend has completed.
func (p *Pool) Release(b *Backend) {
	b.activeConns.Add(-1)
}

// Backends returns the pool's fixed backend set, e.g. so a caller can
// set up one circuit breaker per backend at construction time.
func (p *Pool) Backends() []*Backend {
	return p.backends
}

func (p *Pool) pickRoundRobin() *Backend {
	n := len(p.backends)
	start := int(p.rrCounter.Add(1)) % n
	for i := 0; i < n; i++ {
		b := p.backends[(start+i)%n]
		if b.Healthy() {
			return b
		}
	}
	return nil
}

func (p *Pool) pickLeastConnections() *Backend {
	var best *Backend
	var bestConns int64 = -1
	for _, b := range p.backends {
		if !b.Healthy() {
			continue
		}
		c := b.ActiveConnections()
		if best == nil || c < bestConns {
			best, bestConns = b, c
		}
	}
	return best
}

// pickWeightedRandom builds the healthy candidate list fresh on every
// call so a backend flipping healthy/unhealthy is reflected immediately.
// This is O(n) in the number of backends, which is negligible next to
// the rest of the request path for realistic backend-pool sizes (tens,
// not millions, of instances).
func (p *Pool) pickWeightedRandom() *Backend {
	total := 0
	healthy := make([]*Backend, 0, len(p.backends))
	for _, b := range p.backends {
		if b.Healthy() {
			healthy = append(healthy, b)
			total += b.Weight
		}
	}
	if total == 0 {
		return nil
	}
	r := rand.Intn(total)
	for _, b := range healthy {
		if r < b.Weight {
			return b
		}
		r -= b.Weight
	}
	return healthy[len(healthy)-1]
}
