// Package usage records metered usage cheaply (Redis counters in production)
// and answers limit checks.
package usage

import (
	"context"
	"sync"
)

// Counter is the hot usage-write path. Redis INCR backs it in production.
type Counter interface {
	Incr(ctx context.Context, key string, n int64) (int64, error)
	Value(ctx context.Context, key string) (int64, error)
}

// Snapshotter is an optional capability a Counter may implement so the flusher
// can enumerate accumulated usage. Redis backs this with SCAN; the in-memory
// counter returns a copy of its map.
type Snapshotter interface {
	Snapshot(ctx context.Context) (map[string]int64, error)
}

// MemoryCounter is an in-memory Counter for tests and local runs.
type MemoryCounter struct {
	mu   sync.Mutex
	data map[string]int64
}

// NewMemoryCounter returns an empty counter.
func NewMemoryCounter() *MemoryCounter {
	return &MemoryCounter{data: map[string]int64{}}
}

func (c *MemoryCounter) Incr(_ context.Context, key string, n int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] += n
	return c.data[key], nil
}

func (c *MemoryCounter) Value(_ context.Context, key string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.data[key], nil
}

// Snapshot returns a copy of all current counters (Snapshotter).
func (c *MemoryCounter) Snapshot(_ context.Context) (map[string]int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int64, len(c.data))
	for k, v := range c.data {
		out[k] = v
	}
	return out, nil
}
