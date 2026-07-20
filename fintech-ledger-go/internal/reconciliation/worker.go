// Package reconciliation runs the evening settlement/reconciliation batch:
// matching our ledger's transaction log against the bank's statement file.
package reconciliation

import (
	"context"
	"sync"
)

type Transaction struct {
	ID     string
	Amount int64
}

type Result struct {
	TxnID string
	Err   error
}

// Reconcile processes txns through a fixed-size pool of `workers`
// goroutines, honoring ctx cancellation. A worker pool sized per run and
// torn down at the end (workers exit when `jobs` closes) never leaks: a
// version that spawned one goroutine per transaction with no exit path
// on a stuck downstream call accumulates goroutines across the batch
// until the process runs out of memory by evening.
func Reconcile(ctx context.Context, txns []Transaction, workers int, process func(context.Context, Transaction) error) []Result {
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan Transaction)
	results := make(chan Result)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for txn := range jobs {
				if ctx.Err() != nil {
					results <- Result{TxnID: txn.ID, Err: ctx.Err()}
					continue
				}
				results <- Result{TxnID: txn.ID, Err: process(ctx, txn)}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, t := range txns {
			select {
			case jobs <- t:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	out := make([]Result, 0, len(txns))
	for r := range results {
		out = append(out, r)
	}
	return out
}
