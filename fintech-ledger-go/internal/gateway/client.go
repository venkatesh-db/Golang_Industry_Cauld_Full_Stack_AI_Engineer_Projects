// Package gateway wraps calls to an external payment gateway (UPI/card
// network) with bounded retry.
package gateway

import (
	"context"
	"errors"
	"math/rand"
	"time"
)

var ErrMaxRetriesExceeded = errors.New("gateway: max retries exceeded")

type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{MaxAttempts: 5, BaseDelay: 200 * time.Millisecond, MaxDelay: 5 * time.Second}
}

// CallWithRetry retries call with exponential backoff, jitter, and a hard
// cap on both delay and attempt count. An unbounded retry loop turns a
// transient gateway blip into a self-inflicted request storm — each
// failure retries immediately, multiplying load on a gateway that is
// already struggling. MaxDelay bounds the wait per attempt and
// MaxAttempts bounds the total; only errors explicitly marked Retryable
// are retried at all, since retrying a non-transient error (e.g. a
// declined payment) just wastes attempts.
func CallWithRetry(ctx context.Context, cfg RetryConfig, call func(context.Context) error) error {
	var lastErr error
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(backoff(cfg, attempt)):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		lastErr = call(ctx)
		if lastErr == nil {
			return nil
		}
		if !isRetryable(lastErr) {
			return lastErr
		}
	}
	return errors.Join(ErrMaxRetriesExceeded, lastErr)
}

func backoff(cfg RetryConfig, attempt int) time.Duration {
	d := cfg.BaseDelay << uint(attempt-1)
	if d <= 0 || d > cfg.MaxDelay {
		d = cfg.MaxDelay
	}
	return d/2 + time.Duration(rand.Int63n(int64(d)/2+1))
}

type retryableError struct{ err error }

func (r retryableError) Error() string { return r.err.Error() }
func (r retryableError) Unwrap() error { return r.err }

// Retryable marks err as safe to retry (timeouts, 5xx, connection resets).
// Callers must not mark declines, validation errors, or anything the
// gateway returns as a definitive "no" as retryable.
func Retryable(err error) error { return retryableError{err} }

func isRetryable(err error) bool {
	var re retryableError
	return errors.As(err, &re)
}
