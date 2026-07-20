package reconciliation

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestReconcile_NoGoroutineLeak runs several batches and asserts the
// goroutine count returns to baseline afterward — a per-transaction
// goroutine spawn with no bound and no exit path would leave the count
// growing with every batch.
func TestReconcile_NoGoroutineLeak(t *testing.T) {
	runtime.GC()
	baseline := runtime.NumGoroutine()

	txns := make([]Transaction, 5000)
	for i := range txns {
		txns[i] = Transaction{ID: string(rune('a' + i%26))}
	}

	failEvery3rd := func(ctx context.Context, tx Transaction) error {
		if len(tx.ID)%3 == 0 {
			return errors.New("downstream mismatch")
		}
		return nil
	}

	for i := 0; i < 10; i++ {
		Reconcile(context.Background(), txns, 20, failEvery3rd)
	}

	// Give scheduler a moment to actually unwind exited goroutines.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+2 { // small slack for test runner goroutines
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: baseline=%d, now=%d", baseline, runtime.NumGoroutine())
}

func TestReconcile_RespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	txns := make([]Transaction, 100)
	for i := range txns {
		txns[i] = Transaction{ID: "t"}
	}

	blocked := make(chan struct{})
	var once sync.Once
	process := func(ctx context.Context, tx Transaction) error {
		once.Do(func() { close(blocked) })
		<-ctx.Done()
		return ctx.Err()
	}

	done := make(chan []Result)
	go func() {
		done <- Reconcile(ctx, txns, 2, process)
	}()

	<-blocked
	cancel()

	select {
	case results := <-done:
		if len(results) == 0 {
			t.Fatal("expected at least the in-flight results to be reported")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Reconcile did not return promptly after context cancellation")
	}
}

// TestReconcile_ZeroOrNegativeWorkersDoesNotHang guards against a caller
// passing workers<=0: with no workers draining `jobs`, the feeder
// goroutine would block forever trying to send the first transaction and
// Reconcile would never return.
func TestReconcile_ZeroOrNegativeWorkersDoesNotHang(t *testing.T) {
	txns := []Transaction{{ID: "t1"}, {ID: "t2"}}

	for _, workers := range []int{0, -3} {
		done := make(chan []Result, 1)
		go func() {
			done <- Reconcile(context.Background(), txns, workers, func(context.Context, Transaction) error { return nil })
		}()

		select {
		case results := <-done:
			if len(results) != len(txns) {
				t.Fatalf("workers=%d: got %d results, want %d", workers, len(results), len(txns))
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("Reconcile hung with workers=%d", workers)
		}
	}
}
