package usage

import (
	"context"
	"strings"
	"time"
)

// LedgerEntry is one durable usage record written to the source of truth. It
// carries the delta accrued since the previous flush, not the running total, so
// re-flushing is additive and never double-counts.
type LedgerEntry struct {
	Subject string
	Feature string
	Period  string // e.g. "2026-07"
	Amount  int64  // delta since last flush
}

// LedgerSink is the durable destination for flushed usage (a Postgres
// usage_ledger table in production).
type LedgerSink interface {
	Append(ctx context.Context, e LedgerEntry) error
}

// Flusher periodically drains accumulated Redis counters into the durable
// ledger. It tracks the last-flushed total per key so each flush writes only
// the delta — a crash between flushes loses at most one interval of usage
// (ADR-005), never double-bills.
type Flusher struct {
	counter Snapshotter
	sink    LedgerSink
	flushed map[string]int64 // key -> total already persisted
}

// NewFlusher builds a Flusher over a snapshot-capable counter and a sink.
func NewFlusher(c Snapshotter, sink LedgerSink) *Flusher {
	return &Flusher{counter: c, sink: sink, flushed: map[string]int64{}}
}

// FlushOnce snapshots the counters and appends the per-key delta to the sink.
// It returns the number of ledger entries written (keys with a nonzero delta).
func (f *Flusher) FlushOnce(ctx context.Context) (int, error) {
	snap, err := f.counter.Snapshot(ctx)
	if err != nil {
		return 0, err
	}
	written := 0
	for key, total := range snap {
		delta := total - f.flushed[key]
		if delta == 0 {
			continue
		}
		entry, ok := parseUsageKey(key)
		if !ok {
			continue // ignore keys that aren't usage counters
		}
		entry.Amount = delta
		if err := f.sink.Append(ctx, entry); err != nil {
			return written, err // leave f.flushed unadvanced for this key -> retried next tick
		}
		f.flushed[key] = total
		written++
	}
	return written, nil
}

// Run flushes on a ticker until the context is canceled, then does a final
// flush so shutdown does not strand the last interval of usage.
func (f *Flusher) Run(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_, err := f.FlushOnce(context.WithoutCancel(ctx))
			return err
		case <-t.C:
			if _, err := f.FlushOnce(ctx); err != nil {
				return err
			}
		}
	}
}

// parseUsageKey splits "usage:{subject}:{feature}:{period}". Period is the last
// segment and feature the second-to-last; whatever remains after the "usage:"
// prefix is the subject (which may itself contain colons).
func parseUsageKey(key string) (LedgerEntry, bool) {
	const prefix = "usage:"
	if !strings.HasPrefix(key, prefix) {
		return LedgerEntry{}, false
	}
	rest := strings.TrimPrefix(key, prefix)
	parts := strings.Split(rest, ":")
	if len(parts) < 3 {
		return LedgerEntry{}, false
	}
	period := parts[len(parts)-1]
	feature := parts[len(parts)-2]
	subject := strings.Join(parts[:len(parts)-2], ":")
	if subject == "" || feature == "" || period == "" {
		return LedgerEntry{}, false
	}
	return LedgerEntry{Subject: subject, Feature: feature, Period: period}, true
}
