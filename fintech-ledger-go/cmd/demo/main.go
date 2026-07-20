// Command demo wires the packages together and exercises each fixed
// pattern once, printing what happened. It is not a test — see the
// _test.go files in each package for the actual proofs (race detector,
// deadlock timeout, goroutine-count, balance-conservation assertions).
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"fintechledger/internal/gateway"
	"fintechledger/internal/idempotency"
	"fintechledger/internal/ledger"
	"fintechledger/internal/money"
	"fintechledger/internal/partition"
	"fintechledger/internal/payment"
	"fintechledger/internal/reconciliation"
	"fintechledger/internal/saga"
)

type demoBank struct{ fail bool }

func (b *demoBank) Payout(ctx context.Context, key, acct string, amt money.Paise) error {
	if b.fail {
		return errors.New("bank rail timeout")
	}
	return nil
}
func (b *demoBank) ReversePayout(ctx context.Context, key string) error { return nil }

func main() {
	ctx := context.Background()

	fmt.Println("== ledger.Transfer: deadlock-safe, ordered locking ==")
	a := ledger.NewAccount("A", money.FromRupees(500, 0))
	b := ledger.NewAccount("B", money.FromRupees(500, 0))
	_ = ledger.Transfer(a, b, money.FromRupees(100, 0))
	fmt.Printf("A=%s B=%s (total conserved: %s)\n\n", a.Balance(), b.Balance(), a.Balance().Add(b.Balance()))

	fmt.Println("== payment.Debit: idempotent under client retry ==")
	store := idempotency.NewMemStore()
	accounts := map[string]*ledger.Account{"A": a}
	req := payment.DebitRequest{IdempotencyKey: "req-1", AccountID: "A", Amount: money.FromRupees(50, 0)}
	_, _ = payment.Debit(ctx, store, accounts, req)
	_, _ = payment.Debit(ctx, store, accounts, req) // client retry, same key
	fmt.Printf("A balance after two identical retried requests: %s (debited once)\n\n", a.Balance())

	fmt.Println("== gateway.CallWithRetry: bounded backoff ==")
	attempts := 0
	err := gateway.CallWithRetry(ctx, gateway.RetryConfig{MaxAttempts: 3, BaseDelay: 10 * time.Millisecond, MaxDelay: 50 * time.Millisecond}, func(ctx context.Context) error {
		attempts++
		if attempts < 2 {
			return gateway.Retryable(errors.New("gateway blip"))
		}
		return nil
	})
	fmt.Printf("succeeded after %d attempt(s), err=%v\n\n", attempts, err)

	fmt.Println("== saga.WithdrawToBank: compensation on bank failure ==")
	c := ledger.NewAccount("C", money.FromRupees(1000, 0))
	failingBank := &demoBank{fail: true}
	err = saga.WithdrawToBank(ctx, c, failingBank, "idem-x", "999", money.FromRupees(200, 0))
	fmt.Printf("withdraw error=%v, balance restored to %s\n\n", err, c.Balance())

	fmt.Println("== reconciliation.Reconcile: bounded worker pool ==")
	txns := make([]reconciliation.Transaction, 200)
	for i := range txns {
		txns[i] = reconciliation.Transaction{ID: fmt.Sprintf("t%d", i)}
	}
	results := reconciliation.Reconcile(ctx, txns, 8, func(ctx context.Context, t reconciliation.Transaction) error {
		return nil
	})
	fmt.Printf("reconciled %d transactions with a fixed pool of 8 workers\n\n", len(results))

	fmt.Println("== partition.Ring: single owner per account ==")
	d := ledger.NewAccount("D", money.FromRupees(0, 0))
	shard := partition.NewShard([]*ledger.Account{d})
	defer shard.Close()
	ring := partition.NewRing([]*partition.Shard{shard})
	_ = ring.ShardFor("D").Do(ctx, "D", func(acc *ledger.Account) { acc.Credit(money.FromRupees(10, 0)) })
	fmt.Printf("D balance=%s (mutated only through its owning shard goroutine)\n", d.Balance())
}
