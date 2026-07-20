package idempotency

import (
	"context"
	"testing"
)

func TestMemStore_ReserveThenReplay(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()

	rec, found, err := s.Reserve(ctx, "key-1")
	if err != nil || found || rec != nil {
		t.Fatalf("first Reserve should claim the key: rec=%v found=%v err=%v", rec, found, err)
	}

	if err := s.Complete(ctx, "key-1", Record{Key: "key-1", Body: []byte("debited")}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	rec, found, err = s.Reserve(ctx, "key-1")
	if err != nil || !found {
		t.Fatalf("second Reserve should replay: rec=%v found=%v err=%v", rec, found, err)
	}
	if string(rec.Body) != "debited" {
		t.Fatalf("replayed body = %q, want %q", rec.Body, "debited")
	}
}

func TestMemStore_ConcurrentReserveBlocksSecondCaller(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()

	if _, found, err := s.Reserve(ctx, "key-2"); err != nil || found {
		t.Fatalf("first Reserve unexpected result: found=%v err=%v", found, err)
	}
	if _, _, err := s.Reserve(ctx, "key-2"); err != ErrInProgress {
		t.Fatalf("second concurrent Reserve = %v, want ErrInProgress", err)
	}
}
