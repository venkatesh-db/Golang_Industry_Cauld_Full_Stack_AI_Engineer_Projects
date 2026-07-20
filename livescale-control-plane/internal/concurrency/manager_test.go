package concurrency

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

const farFuture = int64(1) << 62

// admit is a test helper for the common case: session id = device id, now = 0.
func admit(m *Manager, acct, dev string, limit int, exp int64) bool {
	_, ok := m.Admit(acct, dev, limit, 0, exp, "sid-"+dev)
	return ok
}

func TestAdmitExactLimit(t *testing.T) {
	m := New(16)
	for i := 0; i < 3; i++ {
		if !admit(m, "acc", fmt.Sprintf("d%d", i), 3, farFuture) {
			t.Fatalf("device %d should be admitted", i)
		}
	}
	if admit(m, "acc", "d3", 3, farFuture) {
		t.Fatal("4th device must be rejected (exact limit)")
	}
	if got := m.CountFor("acc"); got != 3 {
		t.Fatalf("count = %d, want 3", got)
	}
}

func TestAdmitIdempotentReturnsSameSession(t *testing.T) {
	m := New(16)
	sid1, ok1 := m.Admit("acc", "d0", 2, 0, farFuture, "session-A")
	sid2, ok2 := m.Admit("acc", "d0", 2, 0, farFuture, "session-B") // same device
	if !ok1 || !ok2 {
		t.Fatal("both admits should succeed")
	}
	if sid1 != "session-A" || sid2 != "session-A" {
		t.Fatalf("re-admit must keep original session: got %q, %q", sid1, sid2)
	}
	if got := m.CountFor("acc"); got != 1 {
		t.Fatalf("count = %d, want 1 (idempotent)", got)
	}
}

// TestAdmitRaceNeverOverLimit is the device-limit guardrail: with many
// goroutines racing distinct devices against a small limit, the number admitted
// must never exceed the limit. Run with -race.
func TestAdmitRaceNeverOverLimit(t *testing.T) {
	m := New(16)
	const limit = 5
	const goroutines = 200
	var admitted int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if admit(m, "hot", fmt.Sprintf("dev-%d", i), limit, farFuture) {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if admitted != limit {
		t.Fatalf("admitted %d distinct devices, want exactly %d", admitted, limit)
	}
	if got := m.CountFor("hot"); got != limit {
		t.Fatalf("count = %d, want %d", got, limit)
	}
}

// TestAdmitReapsExpiredBeforeLimit is the M1 guard: an expired device must not
// count against the limit for a new device (no sweeper-lag false rejection).
func TestAdmitReapsExpiredBeforeLimit(t *testing.T) {
	m := New(16)
	past := time.Now().Add(-time.Second).UnixNano()
	// Fill the single slot with a device that is already expired.
	if _, ok := m.Admit("acc", "old", 1, past-1, past, "s-old"); !ok {
		t.Fatal("first admit should succeed")
	}
	// A new device at now should be admitted because "old" is reaped first.
	if _, ok := m.Admit("acc", "new", 1, time.Now().UnixNano(), farFuture, "s-new"); !ok {
		t.Fatal("new device must be admitted after expired one is reaped (M1)")
	}
	if got := m.CountFor("acc"); got != 1 {
		t.Fatalf("count = %d, want 1", got)
	}
}

func TestReleaseAndReap(t *testing.T) {
	m := New(16)
	admit(m, "a", "d0", 5, farFuture)
	admit(m, "a", "d1", 5, time.Now().Add(-time.Second).UnixNano()) // already expired
	m.Release("a", "d0")
	if got := m.CountFor("a"); got != 1 {
		t.Fatalf("after release count = %d, want 1", got)
	}
	if n := m.ReapExpired(time.Now().UnixNano()); n != 1 {
		t.Fatalf("reaped %d, want 1", n)
	}
	if got := m.CountFor("a"); got != 0 {
		t.Fatalf("after reap count = %d, want 0", got)
	}
}

func TestRefreshMissing(t *testing.T) {
	m := New(16)
	if m.Refresh("nope", "d", farFuture) {
		t.Fatal("refresh of missing device must return false")
	}
}
