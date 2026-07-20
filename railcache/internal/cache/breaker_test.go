package cache

import (
	"context"
	"errors"
	"testing"
	"time"
)

// flakyStore lets a test drive success/failure and counts inner calls.
type flakyStore struct {
	err   error
	calls int
}

func (f *flakyStore) GetWithTTL(context.Context, string) ([]byte, time.Duration, error) {
	f.calls++
	return nil, 0, f.err
}
func (f *flakyStore) SetEx(context.Context, string, []byte, time.Duration) error {
	f.calls++
	return f.err
}
func (f *flakyStore) Del(context.Context, string) error { f.calls++; return f.err }
func (f *flakyStore) Acquire(context.Context, string, time.Duration) (Lease, bool, error) {
	f.calls++
	return nil, false, f.err
}

func TestBreakerOpensAfterThresholdAndFailsFast(t *testing.T) {
	inner := &flakyStore{err: errors.New("redis down")}
	var opened, closed int
	b := NewBreaker(inner, 3, 50*time.Millisecond, func(open bool) {
		if open {
			opened++
		} else {
			closed++
		}
	})

	// 3 consecutive transport errors trip the breaker.
	for i := 0; i < 3; i++ {
		if _, _, err := b.GetWithTTL(context.Background(), "k"); err == nil {
			t.Fatalf("call %d: expected error", i)
		}
	}
	if opened != 1 {
		t.Fatalf("opened = %d, want 1", opened)
	}
	callsAtOpen := inner.calls

	// While open, calls fail fast WITHOUT reaching the inner store.
	_, _, err := b.GetWithTTL(context.Background(), "k")
	if !errors.Is(err, ErrBreakerOpen) {
		t.Fatalf("err = %v, want ErrBreakerOpen", err)
	}
	if inner.calls != callsAtOpen {
		t.Fatalf("inner was called while open: %d -> %d", callsAtOpen, inner.calls)
	}
}

func TestBreakerHalfOpensAndClosesOnProbeSuccess(t *testing.T) {
	inner := &flakyStore{err: errors.New("redis down")}
	var closed int
	b := NewBreaker(inner, 2, 20*time.Millisecond, func(open bool) {
		if !open {
			closed++
		}
	})
	for i := 0; i < 2; i++ {
		_, _, _ = b.GetWithTTL(context.Background(), "k")
	}
	// Recover the store, wait out the cooldown; the probe should close the circuit.
	inner.err = nil
	time.Sleep(30 * time.Millisecond)
	if _, _, err := b.GetWithTTL(context.Background(), "k"); err != nil {
		t.Fatalf("probe error = %v, want nil", err)
	}
	if closed != 1 {
		t.Fatalf("closed = %d, want 1", closed)
	}
}

func TestBreakerMissDoesNotTrip(t *testing.T) {
	inner := &flakyStore{err: ErrMiss}
	b := NewBreaker(inner, 2, time.Second, nil)
	for i := 0; i < 10; i++ {
		if _, _, err := b.GetWithTTL(context.Background(), "k"); !errors.Is(err, ErrMiss) {
			t.Fatalf("err = %v, want ErrMiss", err)
		}
	}
	// A miss is a success; the breaker must stay closed and keep calling inner.
	if inner.calls != 10 {
		t.Fatalf("inner calls = %d, want 10 (breaker must not open on misses)", inner.calls)
	}
}
