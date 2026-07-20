package usage

import (
	"context"
	"testing"
)

func TestMeterRecordAndUsed(t *testing.T) {
	m := NewMeter(NewMemoryCounter())
	ctx := context.Background()

	if _, err := m.Record(ctx, "u1", "calls", 3); err != nil {
		t.Fatal(err)
	}
	total, err := m.Record(ctx, "u1", "calls", 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Fatalf("running total = %d, want 5", total)
	}
	used, err := m.Used(ctx, "u1", "calls")
	if err != nil {
		t.Fatal(err)
	}
	if used != 5 {
		t.Fatalf("used = %d, want 5", used)
	}
}
