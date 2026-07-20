package obs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Breaker opens after FailThreshold consecutive failures, then short-circuits.
func TestClient_BreakerOpensAndShortCircuits(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError) // always fail
	}))
	defer srv.Close()

	c := NewClient("test", ClientConfig{
		Timeout:       200 * time.Millisecond,
		MaxRetries:    0, // one attempt per Get, so failures map 1:1 to Gets
		FailThreshold: 3,
		OpenFor:       time.Second,
	})

	// 3 failing calls trip the breaker.
	for i := 0; i < 3; i++ {
		if _, err := c.Get(context.Background(), srv.URL); err == nil {
			t.Fatalf("expected error on attempt %d", i)
		}
	}
	hitsAfterTrip := hits.Load()

	// Next call must short-circuit without touching the server.
	_, err := c.Get(context.Background(), srv.URL)
	if err == nil || !isBreakerOpen(err) {
		t.Fatalf("expected breaker-open error, got %v", err)
	}
	if hits.Load() != hitsAfterTrip {
		t.Fatalf("breaker did not short-circuit: server was hit again")
	}
}

// A success resets the consecutive-failure counter.
func TestClient_SuccessResetsFailures(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient("test", ClientConfig{Timeout: 200 * time.Millisecond, MaxRetries: 0, FailThreshold: 3, OpenFor: time.Second})

	_, _ = c.Get(context.Background(), srv.URL) // 1 failure
	_, _ = c.Get(context.Background(), srv.URL) // 2 failures
	fail.Store(false)
	if _, err := c.Get(context.Background(), srv.URL); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	// Counter reset; two fresh failures should NOT trip (threshold 3).
	fail.Store(true)
	_, _ = c.Get(context.Background(), srv.URL)
	_, _ = c.Get(context.Background(), srv.URL)
	if c.breakerOpen() {
		t.Fatal("breaker opened too early — success did not reset failure count")
	}
}

func isBreakerOpen(err error) bool {
	return err != nil && (err == ErrBreakerOpen || containsBreaker(err.Error()))
}

func containsBreaker(s string) bool {
	return len(s) > 0 && (s == "circuit breaker open" ||
		// wrapped form: "<name>: circuit breaker open"
		len(s) >= len("circuit breaker open") &&
			s[len(s)-len("circuit breaker open"):] == "circuit breaker open")
}
