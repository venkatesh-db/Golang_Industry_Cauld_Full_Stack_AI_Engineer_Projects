package payment

import (
	"context"
	"testing"

	"fintechledger/internal/idempotency"
	"fintechledger/internal/ledger"
	"fintechledger/internal/money"
)

// TestDebit_RetryWithSameKeyDoesNotDoubleDebit simulates the classic
// incident: the client's first request succeeds but the response is
// lost (network blip), so the client retries with the same
// Idempotency-Key. The account must only be debited once.
func TestDebit_RetryWithSameKeyDoesNotDoubleDebit(t *testing.T) {
	acc := ledger.NewAccount("acct-1", money.FromRupees(1000, 0))
	accounts := map[string]*ledger.Account{"acct-1": acc}
	store := idempotency.NewMemStore()
	ctx := context.Background()

	req := DebitRequest{IdempotencyKey: "req-abc", AccountID: "acct-1", Amount: money.FromRupees(100, 0)}

	if _, err := Debit(ctx, store, accounts, req); err != nil {
		t.Fatalf("first Debit: %v", err)
	}
	if _, err := Debit(ctx, store, accounts, req); err != nil {
		t.Fatalf("retried Debit: %v", err)
	}
	if _, err := Debit(ctx, store, accounts, req); err != nil {
		t.Fatalf("third retried Debit: %v", err)
	}

	want := money.FromRupees(900, 0)
	if got := acc.Balance(); got != want {
		t.Fatalf("balance = %s, want %s (account was debited more than once)", got, want)
	}
}

func TestDebit_DifferentKeysDebitIndependently(t *testing.T) {
	acc := ledger.NewAccount("acct-1", money.FromRupees(1000, 0))
	accounts := map[string]*ledger.Account{"acct-1": acc}
	store := idempotency.NewMemStore()
	ctx := context.Background()

	for _, key := range []string{"req-1", "req-2"} {
		req := DebitRequest{IdempotencyKey: key, AccountID: "acct-1", Amount: money.FromRupees(100, 0)}
		if _, err := Debit(ctx, store, accounts, req); err != nil {
			t.Fatalf("Debit(%s): %v", key, err)
		}
	}

	want := money.FromRupees(800, 0)
	if got := acc.Balance(); got != want {
		t.Fatalf("balance = %s, want %s", got, want)
	}
}

func TestDebit_RequiresIdempotencyKey(t *testing.T) {
	store := idempotency.NewMemStore()
	_, err := Debit(context.Background(), store, nil, DebitRequest{AccountID: "acct-1"})
	if err != ErrMissingIdempotencyKey {
		t.Fatalf("got %v, want ErrMissingIdempotencyKey", err)
	}
}
