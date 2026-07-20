// Package cache is a sharded, TTL-bounded, size-bounded read-through
// cache fronting slow backend lookups (merchant config, KYC status,
// exchange rates) that don't need to hit the backend on every request.
//
// Like ratelimit, it shards by key across independent locked segments
// (with an LRU eviction list per shard) so that unrelated keys don't
// contend on one global lock — the dominant cost at high QPS.
package cache

import (
	"container/list"
	"hash/fnv"
	"sync"
	"time"
)

const defaultShardCount = 256

type entry struct {
	key       string
	value     any
	expiresAt time.Time
	elem      *list.Element
}

type shard struct {
	mu       sync.Mutex
	items    map[string]*entry
	order    *list.List // front = most recently used
	capacity int
}

// Cache is a sharded LRU cache with per-entry TTL.
type Cache struct {
	shards []*shard
	ttl    time.Duration
	now    func() time.Time
}

// New creates a cache with the given per-shard capacity (so total
// capacity is roughly capacityPerShard * 256) and a default TTL applied
// to every Set.
func New(capacityPerShard int, ttl time.Duration) *Cache {
	return newWithClock(capacityPerShard, ttl, time.Now)
}

func newWithClock(capacityPerShard int, ttl time.Duration, now func() time.Time) *Cache {
	shards := make([]*shard, defaultShardCount)
	for i := range shards {
		shards[i] = &shard{
			items:    make(map[string]*entry),
			order:    list.New(),
			capacity: capacityPerShard,
		}
	}
	return &Cache{shards: shards, ttl: ttl, now: now}
}

func (c *Cache) shardFor(key string) *shard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return c.shards[h.Sum32()%uint32(len(c.shards))]
}

// Get returns the cached value for key if present and not expired.
func (c *Cache) Get(key string) (any, bool) {
	s := c.shardFor(key)
	now := c.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.items[key]
	if !ok {
		return nil, false
	}
	if now.After(e.expiresAt) {
		s.order.Remove(e.elem)
		delete(s.items, key)
		return nil, false
	}
	s.order.MoveToFront(e.elem)
	return e.value, true
}

// Set stores value for key with the cache's default TTL, evicting the
// least-recently-used entry in that key's shard if it's over capacity.
func (c *Cache) Set(key string, value any) {
	s := c.shardFor(key)
	now := c.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	if e, ok := s.items[key]; ok {
		e.value = value
		e.expiresAt = now.Add(c.ttl)
		s.order.MoveToFront(e.elem)
		return
	}

	e := &entry{key: key, value: value, expiresAt: now.Add(c.ttl)}
	e.elem = s.order.PushFront(e)
	s.items[key] = e

	if s.capacity > 0 && len(s.items) > s.capacity {
		oldest := s.order.Back()
		if oldest != nil {
			old := oldest.Value.(*entry)
			s.order.Remove(oldest)
			delete(s.items, old.key)
		}
	}
}

// GetOrLoad returns the cached value, or calls load to compute and cache
// it on a miss. load runs while holding the owning shard's lock, so a
// concurrent miss for the *same key* never triggers a duplicate load —
// it blocks until the first call finishes and then finds the value
// already cached. This is coarser than per-key single-flight: a slow
// load also blocks unrelated keys that happen to hash to the same
// shard. That's an acceptable trade at 256 shards for fast loads (a
// cache warmer calling a backend), but load must never block for long
// or call back into this same Cache — either can stall the shard.
func (c *Cache) GetOrLoad(key string, load func() (any, error)) (any, error) {
	if v, ok := c.Get(key); ok {
		return v, nil
	}

	s := c.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	// Re-check under the shard lock: another goroutine may have loaded
	// this key while we were waiting for the lock.
	now := c.now()
	if e, ok := s.items[key]; ok && now.Before(e.expiresAt) {
		s.order.MoveToFront(e.elem)
		return e.value, nil
	}

	value, err := load()
	if err != nil {
		return nil, err
	}

	if e, ok := s.items[key]; ok {
		e.value = value
		e.expiresAt = now.Add(c.ttl)
		s.order.MoveToFront(e.elem)
	} else {
		e := &entry{key: key, value: value, expiresAt: now.Add(c.ttl)}
		e.elem = s.order.PushFront(e)
		s.items[key] = e
		if s.capacity > 0 && len(s.items) > s.capacity {
			oldest := s.order.Back()
			if oldest != nil {
				old := oldest.Value.(*entry)
				s.order.Remove(oldest)
				delete(s.items, old.key)
			}
		}
	}
	return value, nil
}
