package chaos

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// Disabled registry ignores injectors and registers no routes.
func TestRegistry_DisabledIsInert(t *testing.T) {
	r := New(false)
	if r.Enabled() {
		t.Fatal("expected disabled")
	}
	r.SlowDep(500)
	if r.SlowDepDelay() != 500*time.Millisecond {
		t.Fatal("state still settable, but routes must not register (checked in Register)")
	}
}

// SlowDep and RetryStorm toggle the request-path state, Reset clears it.
func TestRegistry_SlowDepAndReset(t *testing.T) {
	r := New(true)
	r.SlowDep(800)
	r.RetryStorm(true)
	if r.SlowDepDelay() != 800*time.Millisecond {
		t.Fatalf("slow dep not set: %v", r.SlowDepDelay())
	}
	if !r.ShouldFail() {
		t.Fatal("retry storm not set")
	}
	r.Reset()
	if r.SlowDepDelay() != 0 || r.ShouldFail() {
		t.Fatal("reset did not clear request-path state")
	}
}

// GoroutineLeak spawns goroutines; Reset releases them.
func TestRegistry_GoroutineLeakAndReset(t *testing.T) {
	r := New(true)
	before := runtime.NumGoroutine()
	r.GoroutineLeak(50)
	// Give the scheduler a moment to start them.
	time.Sleep(20 * time.Millisecond)
	during := runtime.NumGoroutine()
	if during <= before {
		t.Fatalf("expected goroutines to climb: before=%d during=%d", before, during)
	}
	r.Reset()
	// Released goroutines exit; poll until they drain (or timeout).
	deadline := time.Now().Add(time.Second)
	for runtime.NumGoroutine() > during-40 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	after := runtime.NumGoroutine()
	if after >= during {
		t.Fatalf("reset did not release goroutines: during=%d after=%d", during, after)
	}
}

// MemPressure retains allocations; Reset drops them (best-effort, no crash).
func TestRegistry_MemPressureAndReset(t *testing.T) {
	r := New(true)
	r.MemPressure(8) // 8 MB
	r.Reset()        // must not panic; references dropped
	_ = context.Background()
}
