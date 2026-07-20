package usage

import (
	"context"
	"fmt"
	"time"
)

// Meter records per-period metered usage and reads current usage. Durable
// flushing to a Postgres usage_ledger is a separate worker (not in this base).
type Meter struct {
	counter Counter
	now     func() time.Time
}

// NewMeter builds a Meter over a Counter.
func NewMeter(c Counter) *Meter {
	return &Meter{counter: c, now: time.Now}
}

// key namespaces usage by subject, feature, and monthly period.
func (m *Meter) key(subject, feature string) string {
	period := m.now().UTC().Format("2006-01")
	return fmt.Sprintf("usage:%s:%s:%s", subject, feature, period)
}

// Record increments usage for the current period and returns the new total.
func (m *Meter) Record(ctx context.Context, subject, feature string, n int64) (int64, error) {
	return m.counter.Incr(ctx, m.key(subject, feature), n)
}

// Used returns current-period usage for a subject+feature.
func (m *Meter) Used(ctx context.Context, subject, feature string) (int64, error) {
	return m.counter.Value(ctx, m.key(subject, feature))
}
