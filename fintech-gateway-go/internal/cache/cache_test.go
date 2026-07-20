package cache

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetSet_RoundTrip(t *testing.T) {
	c := New(100, time.Minute)
	c.Set("k1", "v1")
	v, ok := c.Get("k1")
	if !ok || v != "v1" {
		t.Fatalf("Get(k1) = %v, %v; want v1, true", v, ok)
	}
}

func TestGet_MissReturnsFalse(t *testing.T) {
	c := New(100, time.Minute)
	if _, ok := c.Get("missing"); ok {
		t.Fatal("expected miss on unset key")
	}
}

func TestGet_ExpiresAfterTTL(t *testing.T) {
	fake := time.Now()
	c := newWithClock(100, time.Second, func() time.Time { return fake })
	c.Set("k1", "v1")

	fake = fake.Add(2 * time.Second)
	if _, ok := c.Get("k1"); ok {
		t.Fatal("expected entry to be expired")
	}
}

// TestSet_EvictsLeastRecentlyUsedWithinShard proves LRU eviction: with
// capacity 2 on a single shard, inserting a 3rd key evicts the least
// recently touched one, not an arbitrary one.
func TestSet_EvictsLeastRecentlyUsedWithinShard(t *testing.T) {
	c := New(2, time.Hour)
	// Force all three keys onto the same shard by reusing the cache's
	// own routing and picking keys that hash there — simpler: shrink to
	// a single shard for this test via direct construction.
	c.shards = c.shards[:1]
	for i := range c.shards {
		c.shards[i].capacity = 2
	}

	c.Set("a", 1)
	c.Set("b", 2)
	c.Get("a") // touch a, making b the least recently used
	c.Set("c", 3)

	if _, ok := c.Get("b"); ok {
		t.Fatal("expected b (least recently used) to have been evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("expected a (recently touched) to survive eviction")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("expected newly inserted c to be present")
	}
}

// TestGetOrLoad_CollapsesConcurrentMissesForSameKey proves the
// stampede-protection guarantee: 100 concurrent misses on the same key
// call load exactly once.
func TestGetOrLoad_CollapsesConcurrentMissesForSameKey(t *testing.T) {
	c := New(100, time.Minute)
	var loadCalls int64

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.GetOrLoad("hot-key", func() (any, error) {
				atomic.AddInt64(&loadCalls, 1)
				time.Sleep(5 * time.Millisecond)
				return "loaded", nil
			})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&loadCalls); got != 1 {
		t.Fatalf("load was called %d times, want exactly 1", got)
	}
}

func TestGetOrLoad_PropagatesLoadError(t *testing.T) {
	c := New(100, time.Minute)
	wantErr := fmt.Errorf("backend unavailable")
	_, err := c.GetOrLoad("k1", func() (any, error) { return nil, wantErr })
	if err != wantErr {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
	if _, ok := c.Get("k1"); ok {
		t.Fatal("a failed load must not populate the cache")
	}
}

func BenchmarkGet_Hit(b *testing.B) {
	c := New(10000, time.Minute)
	c.Set("k1", "v1")
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Get("k1")
		}
	})
}

func BenchmarkSet(b *testing.B) {
	c := New(10000, time.Minute)
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Set(fmt.Sprintf("k%d", i%1000), i)
			i++
		}
	})
}
