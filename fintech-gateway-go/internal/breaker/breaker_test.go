package breaker

import (
	"sync"
	"testing"
	"time"
)

func TestBreaker_TripsAfterConsecutiveFailures(t *testing.T) {
	b := New(Config{FailureThreshold: 3, OpenDuration: time.Hour, HalfOpenSuccessThreshold: 1, MaxHalfOpenRequests: 1})

	for i := 0; i < 3; i++ {
		if !b.Allow() {
			t.Fatalf("request %d should be allowed while closed", i)
		}
		b.Failure()
	}

	if b.Allow() {
		t.Fatal("breaker should be open after FailureThreshold consecutive failures")
	}
}

func TestBreaker_SuccessResetsFailureStreak(t *testing.T) {
	b := New(Config{FailureThreshold: 3, OpenDuration: time.Hour, HalfOpenSuccessThreshold: 1, MaxHalfOpenRequests: 1})

	b.Allow()
	b.Failure()
	b.Allow()
	b.Failure()
	b.Allow()
	b.Success() // resets the streak
	b.Allow()
	b.Failure()

	if !b.Allow() {
		t.Fatal("breaker should still be closed: only 1 consecutive failure since the reset")
	}
}

// TestBreaker_RecoversThroughHalfOpen is the regression test for the
// stuck-forever bug: with HalfOpenSuccessThreshold=2, the breaker must
// actually be able to observe 2 successes (not just let a single trial
// request through and then wedge) and close again.
func TestBreaker_RecoversThroughHalfOpen(t *testing.T) {
	fake := time.Now()
	b := newWithClock(Config{
		FailureThreshold:         1,
		OpenDuration:             time.Second,
		HalfOpenSuccessThreshold: 2,
		MaxHalfOpenRequests:      5,
	}, func() time.Time { return fake })

	b.Allow()
	b.Failure() // trips open
	if b.Allow() {
		t.Fatal("should be open immediately after tripping")
	}

	fake = fake.Add(2 * time.Second) // past OpenDuration

	successes := 0
	for i := 0; i < 5; i++ {
		if b.Allow() {
			successes++
			b.Success()
		}
	}
	if successes < 2 {
		t.Fatalf("only %d trial requests were let through in half-open, need >= HalfOpenSuccessThreshold=2 to ever close", successes)
	}

	if !b.Allow() {
		t.Fatal("breaker should be closed again after enough half-open successes")
	}
}

func TestBreaker_HalfOpenFailureReopensImmediately(t *testing.T) {
	fake := time.Now()
	b := newWithClock(Config{
		FailureThreshold:         1,
		OpenDuration:             time.Second,
		HalfOpenSuccessThreshold: 3,
		MaxHalfOpenRequests:      5,
	}, func() time.Time { return fake })

	b.Allow()
	b.Failure() // trips open
	fake = fake.Add(2 * time.Second)

	if !b.Allow() {
		t.Fatal("expected the first half-open trial to be allowed")
	}
	b.Failure() // trial fails -> reopen

	if b.Allow() {
		t.Fatal("breaker should be open again immediately after a failed half-open trial")
	}
}

// TestBreaker_HalfOpenBoundsTrialRequests holds the threshold below the
// cap and never reports Success, so the breaker stays in HalfOpen for
// the whole loop — isolating whether Allow enforces MaxHalfOpenRequests
// on its own, separately from the close-on-enough-successes path.
func TestBreaker_HalfOpenBoundsTrialRequests(t *testing.T) {
	fake := time.Now()
	b := newWithClock(Config{
		FailureThreshold:         1,
		OpenDuration:             time.Second,
		HalfOpenSuccessThreshold: 1,
		MaxHalfOpenRequests:      3,
	}, func() time.Time { return fake })

	b.Allow()
	b.Failure()
	fake = fake.Add(2 * time.Second)

	allowed := 0
	for i := 0; i < 20; i++ {
		if b.Allow() {
			allowed++
		}
	}
	if allowed > 3 {
		t.Fatalf("half-open let %d requests through, want capped at MaxHalfOpenRequests=3", allowed)
	}
}

// TestBreaker_ConcurrentAllowSuccessFailure must pass under -race.
func TestBreaker_ConcurrentAllowSuccessFailure(t *testing.T) {
	b := New(DefaultConfig())
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if b.Allow() {
				if i%3 == 0 {
					b.Failure()
				} else {
					b.Success()
				}
			}
		}(i)
	}
	wg.Wait()
}
