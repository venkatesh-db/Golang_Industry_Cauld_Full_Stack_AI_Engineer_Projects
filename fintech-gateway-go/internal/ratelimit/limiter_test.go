package ratelimit

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestAllow_BurstThenBlocks(t *testing.T) {
	fake := time.Now()
	l := newWithClock(1, 5, func() time.Time { return fake })
	defer l.Close()

	for i := 0; i < 5; i++ {
		if !l.Allow("caller-1") {
			t.Fatalf("request %d within burst should be allowed", i)
		}
	}
	if l.Allow("caller-1") {
		t.Fatal("request beyond burst should be blocked")
	}
}

func TestAllow_RefillsOverTime(t *testing.T) {
	fake := time.Now()
	l := newWithClock(10, 1, func() time.Time { return fake })
	defer l.Close()

	if !l.Allow("caller-1") {
		t.Fatal("first request should be allowed")
	}
	if l.Allow("caller-1") {
		t.Fatal("second immediate request should be blocked (burst=1)")
	}

	fake = fake.Add(200 * time.Millisecond) // 10/s * 0.2s = 2 tokens, capped at burst=1
	if !l.Allow("caller-1") {
		t.Fatal("request after refill window should be allowed")
	}
}

func TestAllow_DifferentKeysAreIndependent(t *testing.T) {
	fake := time.Now()
	l := newWithClock(1, 1, func() time.Time { return fake })
	defer l.Close()

	if !l.Allow("caller-1") || !l.Allow("caller-2") {
		t.Fatal("distinct keys must not share a bucket")
	}
}

// TestAllow_ConcurrentSameKey must pass under -race and proves a single
// key's bucket is never over- or under-counted under concurrent access.
// The clock is held fixed so no real elapsed time can add extra tokens
// mid-test and make the exact-count assertion below flaky.
func TestAllow_ConcurrentSameKey(t *testing.T) {
	fake := time.Now()
	l := newWithClock(1000, 1000, func() time.Time { return fake })
	defer l.Close()

	var wg sync.WaitGroup
	results := make([]bool, 2000)
	for i := 0; i < 2000; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = l.Allow("shared-key")
		}(i)
	}
	wg.Wait()

	allowed := 0
	for _, r := range results {
		if r {
			allowed++
		}
	}
	if allowed != 1000 {
		t.Fatalf("allowed = %d, want exactly burst=1000 (sharding must not let a key exceed its own bucket)", allowed)
	}
}

// TestSweep_EvictsIdleKeys proves the memory-bound guarantee: a key that
// is never queried again after going idle is still reclaimed by the
// background sweeper, not just by opportunistic eviction on access.
func TestSweep_EvictsIdleKeys(t *testing.T) {
	fake := time.Now()
	l := newWithClock(1, 1, func() time.Time { return fake })
	defer l.Close()

	l.Allow("abandoned-caller")

	total := 0
	for _, s := range l.shards {
		s.mu.Lock()
		total += len(s.buckets)
		s.mu.Unlock()
	}
	if total != 1 {
		t.Fatalf("expected 1 bucket after first Allow, got %d", total)
	}

	fake = fake.Add(l.idleTTL + time.Second)
	l.sweep()

	total = 0
	for _, s := range l.shards {
		s.mu.Lock()
		total += len(s.buckets)
		s.mu.Unlock()
	}
	if total != 0 {
		t.Fatalf("expected sweep to evict the idle bucket, got %d remaining", total)
	}
}

// TestClose_StopsSweeperGoroutine guards against the sweeper itself
// leaking: every Limiter must be fully stoppable.
func TestClose_StopsSweeperGoroutine(t *testing.T) {
	runtime.GC()
	baseline := runtime.NumGoroutine()

	for i := 0; i < 20; i++ {
		l := New(10, 10)
		l.Close()
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("sweeper goroutine leak: baseline=%d, now=%d", baseline, runtime.NumGoroutine())
}

func BenchmarkAllow(b *testing.B) {
	l := New(1_000_000, 1000)
	defer l.Close()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			l.Allow(keyFor(i))
			i++
		}
	})
}

func keyFor(i int) string {
	keys := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	return keys[i%len(keys)]
}
