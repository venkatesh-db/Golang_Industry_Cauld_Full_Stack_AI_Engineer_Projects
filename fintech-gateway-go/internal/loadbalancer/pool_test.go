package loadbalancer

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestRoundRobin_DistributesEvenly(t *testing.T) {
	backends := []*Backend{NewBackend("a", "", 1), NewBackend("b", "", 1), NewBackend("c", "", 1)}
	p, err := NewPool(RoundRobin, backends)
	if err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	for i := 0; i < 300; i++ {
		b, err := p.Pick("")
		if err != nil {
			t.Fatal(err)
		}
		counts[b.ID]++
		p.Release(b)
	}
	for _, id := range []string{"a", "b", "c"} {
		if counts[id] != 100 {
			t.Fatalf("backend %s got %d picks, want exactly 100 (perfect round-robin over 300 picks / 3 backends)", id, counts[id])
		}
	}
}

func TestRoundRobin_SkipsUnhealthyBackends(t *testing.T) {
	a, b, c := NewBackend("a", "", 1), NewBackend("b", "", 1), NewBackend("c", "", 1)
	b.SetHealthy(false)
	p, _ := NewPool(RoundRobin, []*Backend{a, b, c})

	for i := 0; i < 20; i++ {
		picked, err := p.Pick("")
		if err != nil {
			t.Fatal(err)
		}
		if picked.ID == "b" {
			t.Fatal("unhealthy backend must never be picked")
		}
		p.Release(picked)
	}
}

func TestRoundRobin_AllUnhealthyReturnsError(t *testing.T) {
	a := NewBackend("a", "", 1)
	a.SetHealthy(false)
	p, _ := NewPool(RoundRobin, []*Backend{a})
	if _, err := p.Pick(""); err != ErrNoHealthyBackends {
		t.Fatalf("got %v, want ErrNoHealthyBackends", err)
	}
}

func TestLeastConnections_PicksTheLeastLoaded(t *testing.T) {
	a, b := NewBackend("a", "", 1), NewBackend("b", "", 1)
	p, _ := NewPool(LeastConnections, []*Backend{a, b})

	a.activeConns.Store(5)
	b.activeConns.Store(0)

	picked, err := p.Pick("")
	if err != nil {
		t.Fatal(err)
	}
	if picked.ID != "b" {
		t.Fatalf("picked %s, want b (fewer active connections)", picked.ID)
	}
}

func TestWeightedRandom_RespectsWeightRatio(t *testing.T) {
	heavy, light := NewBackend("heavy", "", 9), NewBackend("light", "", 1)
	p, _ := NewPool(WeightedRandom, []*Backend{heavy, light})

	counts := map[string]int{}
	const n = 10000
	for i := 0; i < n; i++ {
		b, err := p.Pick("")
		if err != nil {
			t.Fatal(err)
		}
		counts[b.ID]++
		p.Release(b)
	}

	ratio := float64(counts["heavy"]) / float64(n)
	if ratio < 0.85 || ratio > 0.95 {
		t.Fatalf("heavy backend got %.2f%% of traffic, want ~90%% (weight 9 vs 1)", ratio*100)
	}
}

func TestConsistentHash_SameKeyAlwaysSameBackend(t *testing.T) {
	backends := []*Backend{NewBackend("a", "", 1), NewBackend("b", "", 1), NewBackend("c", "", 1)}
	p, _ := NewPool(ConsistentHash, backends)

	first, err := p.Pick("account-42")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		b, err := p.Pick("account-42")
		if err != nil {
			t.Fatal(err)
		}
		if b.ID != first.ID {
			t.Fatalf("routing for the same key must be stable, got %s then %s", first.ID, b.ID)
		}
		p.Release(b)
	}
}

func TestConsistentHash_SkipsUnhealthyBackend(t *testing.T) {
	backends := []*Backend{NewBackend("a", "", 1), NewBackend("b", "", 1), NewBackend("c", "", 1)}
	p, _ := NewPool(ConsistentHash, backends)

	first, err := p.Pick("account-42")
	if err != nil {
		t.Fatal(err)
	}
	p.Release(first)
	first.SetHealthy(false)

	rerouted, err := p.Pick("account-42")
	if err != nil {
		t.Fatal(err)
	}
	if rerouted.ID == first.ID {
		t.Fatal("expected key to reroute away from the now-unhealthy backend")
	}
}

// TestPool_ConcurrentPickRelease must pass under -race across all
// strategies.
func TestPool_ConcurrentPickRelease(t *testing.T) {
	for _, strategy := range []Strategy{RoundRobin, LeastConnections, WeightedRandom, ConsistentHash} {
		backends := []*Backend{NewBackend("a", "", 2), NewBackend("b", "", 1), NewBackend("c", "", 3)}
		p, err := NewPool(strategy, backends)
		if err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		for i := 0; i < 500; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				b, err := p.Pick("some-key")
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				p.Release(b)
			}()
		}
		wg.Wait()
	}
}

func TestHealthChecker_UpdatesBackendHealth(t *testing.T) {
	b := NewBackend("a", "", 1)
	hc := StartHealthChecker([]*Backend{b}, 5*time.Millisecond, func(*Backend) bool { return false })
	defer hc.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !b.Healthy() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("health checker never marked the backend unhealthy")
}

func TestHealthChecker_CloseStopsGoroutine(t *testing.T) {
	runtime.GC()
	baseline := runtime.NumGoroutine()

	for i := 0; i < 20; i++ {
		hc := StartHealthChecker(nil, time.Millisecond, func(*Backend) bool { return true })
		hc.Close()
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("health checker goroutine leak: baseline=%d, now=%d", baseline, runtime.NumGoroutine())
}

func BenchmarkPick_RoundRobin(b *testing.B) {
	backends := []*Backend{NewBackend("a", "", 1), NewBackend("b", "", 1), NewBackend("c", "", 1)}
	p, _ := NewPool(RoundRobin, backends)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			backend, _ := p.Pick("")
			p.Release(backend)
		}
	})
}

func BenchmarkPick_ConsistentHash(b *testing.B) {
	backends := []*Backend{NewBackend("a", "", 1), NewBackend("b", "", 1), NewBackend("c", "", 1)}
	p, _ := NewPool(ConsistentHash, backends)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			backend, _ := p.Pick("account-42")
			p.Release(backend)
		}
	})
}
