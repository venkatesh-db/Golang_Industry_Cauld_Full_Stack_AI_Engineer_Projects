package order

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidOrder        = errors.New("invalid order")
	ErrIdempotencyConflict = errors.New("idempotency key was used for a different order")
	ErrOrderNotFound       = errors.New("order not found")
	ErrLeaseLost           = errors.New("outbox event lease was lost")
)

// TransientError marks an outcome that was not known. Its operation must be
// retried with the exact same external idempotency key.
type TransientError struct{ Cause error }

func (e TransientError) Error() string { return e.Cause.Error() }
func (e TransientError) Unwrap() error { return e.Cause }
func (e TransientError) Temporary() bool { return true }

func transient(err error) error {
	if err == nil {
		return nil
	}
	return TransientError{Cause: err}
}

func isTransient(err error) bool {
	var target interface{ Temporary() bool }
	return errors.As(err, &target) && target.Temporary()
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOrder, fmt.Sprintf(format, args...))
}
