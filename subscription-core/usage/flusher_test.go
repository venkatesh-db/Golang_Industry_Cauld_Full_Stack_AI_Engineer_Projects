package usage

import (
	"context"
	"sync"
	"testing"
)

// memSink collects appended ledger entries.
type memSink struct {
	mu      sync.Mutex
	entries []LedgerEntry
}

func (s *memSink) Append(_ context.Context, e LedgerEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return nil
}

func TestFlushOnce_WritesDeltasOnly(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCounter()
	sink := &memSink{}
	f := NewFlusher(c, sink)
	m := NewMeter(c)

	// Accrue 5 then flush -> one entry of 5.
	if _, err := m.Record(ctx, "user_1", "api_calls", 5); err != nil {
		t.Fatal(err)
	}
	n, err := f.FlushOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("first flush wrote %d entries, want 1", n)
	}
	if got := sink.entries[0].Amount; got != 5 {
		t.Errorf("delta = %d, want 5", got)
	}

	// Flush again with no new usage -> nothing written (idempotent).
	n, _ = f.FlushOnce(ctx)
	if n != 0 {
		t.Fatalf("no-op flush wrote %d entries, want 0", n)
	}

	// Accrue 3 more -> delta of 3, not the running total of 8.
	if _, err := m.Record(ctx, "user_1", "api_calls", 3); err != nil {
		t.Fatal(err)
	}
	n, _ = f.FlushOnce(ctx)
	if n != 1 {
		t.Fatalf("delta flush wrote %d entries, want 1", n)
	}
	if got := sink.entries[1].Amount; got != 3 {
		t.Errorf("second delta = %d, want 3 (must not double-count)", got)
	}
}

func TestFlushOnce_ParsesKeyFields(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCounter()
	sink := &memSink{}
	f := NewFlusher(c, sink)
	m := NewMeter(c)

	if _, err := m.Record(ctx, "user_42", "exports", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.FlushOnce(ctx); err != nil {
		t.Fatal(err)
	}
	e := sink.entries[0]
	if e.Subject != "user_42" || e.Feature != "exports" {
		t.Errorf("parsed entry = %+v, want subject=user_42 feature=exports", e)
	}
	if e.Period == "" {
		t.Error("period should be populated from the counter key")
	}
}

func TestParseUsageKey(t *testing.T) {
	cases := []struct {
		key     string
		ok      bool
		subject string
		feature string
		period  string
	}{
		{"usage:user_1:api_calls:2026-07", true, "user_1", "api_calls", "2026-07"},
		{"usage:tenant:a:b:seats:2026-07", true, "tenant:a:b", "seats", "2026-07"}, // subject with colons
		{"notusage:x:y:z", false, "", "", ""},
		{"usage:onlyone", false, "", "", ""},
	}
	for _, c := range cases {
		got, ok := parseUsageKey(c.key)
		if ok != c.ok {
			t.Errorf("parseUsageKey(%q) ok = %v, want %v", c.key, ok, c.ok)
			continue
		}
		if ok && (got.Subject != c.subject || got.Feature != c.feature || got.Period != c.period) {
			t.Errorf("parseUsageKey(%q) = %+v, want subject=%q feature=%q period=%q",
				c.key, got, c.subject, c.feature, c.period)
		}
	}
}
