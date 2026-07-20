package gateway

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCallWithRetry_SucceedsAfterTransientFailures(t *testing.T) {
	attempts := 0
	err := CallWithRetry(context.Background(), RetryConfig{MaxAttempts: 5, BaseDelay: time.Millisecond, MaxDelay: 10 * time.Millisecond}, func(ctx context.Context) error {
		attempts++
		if attempts < 3 {
			return Retryable(errors.New("timeout"))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestCallWithRetry_StopsAtMaxAttempts(t *testing.T) {
	attempts := 0
	err := CallWithRetry(context.Background(), RetryConfig{MaxAttempts: 4, BaseDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond}, func(ctx context.Context) error {
		attempts++
		return Retryable(errors.New("still failing"))
	})
	if !errors.Is(err, ErrMaxRetriesExceeded) {
		t.Fatalf("got %v, want ErrMaxRetriesExceeded", err)
	}
	if attempts != 4 {
		t.Fatalf("attempts = %d, want exactly MaxAttempts=4 (no unbounded retry storm)", attempts)
	}
}

func TestCallWithRetry_NonRetryableFailsImmediately(t *testing.T) {
	attempts := 0
	declineErr := errors.New("card declined")
	err := CallWithRetry(context.Background(), DefaultRetryConfig(), func(ctx context.Context) error {
		attempts++
		return declineErr
	})
	if !errors.Is(err, declineErr) {
		t.Fatalf("got %v, want declineErr", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (non-retryable errors must not retry)", attempts)
	}
}
