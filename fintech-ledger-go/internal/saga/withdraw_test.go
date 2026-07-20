package saga

import (
	"context"
	"errors"
	"testing"

	"fintechledger/internal/ledger"
	"fintechledger/internal/money"
)

type fakeBank struct {
	failPayout bool
	reversed   bool
	paidOut    bool
}

func (b *fakeBank) Payout(ctx context.Context, idempotencyKey, bankAccountNo string, amt money.Paise) error {
	if b.failPayout {
		return errors.New("bank rail timeout")
	}
	b.paidOut = true
	return nil
}

func (b *fakeBank) ReversePayout(ctx context.Context, idempotencyKey string) error {
	b.reversed = true
	return nil
}

func TestWithdrawToBank_Success(t *testing.T) {
	acc := ledger.NewAccount("wallet-1", money.FromRupees(1000, 0))
	bank := &fakeBank{}

	err := WithdrawToBank(context.Background(), acc, bank, "idem-1", "1234567890", money.FromRupees(300, 0))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bank.paidOut {
		t.Fatal("expected bank payout to have been called")
	}
	want := money.FromRupees(700, 0)
	if got := acc.Balance(); got != want {
		t.Fatalf("balance = %s, want %s", got, want)
	}
}

// TestWithdrawToBank_BankFailureCompensatesWalletDebit is the saga's
// core guarantee: when the bank leg fails after the wallet debit already
// committed, the compensating credit restores the original balance —
// there is never a window where money left the wallet with no
// corresponding bank payout and no reversal.
func TestWithdrawToBank_BankFailureCompensatesWalletDebit(t *testing.T) {
	acc := ledger.NewAccount("wallet-1", money.FromRupees(1000, 0))
	bank := &fakeBank{failPayout: true}

	err := WithdrawToBank(context.Background(), acc, bank, "idem-2", "1234567890", money.FromRupees(300, 0))
	if err == nil {
		t.Fatal("expected error from failed bank payout")
	}

	want := money.FromRupees(1000, 0)
	if got := acc.Balance(); got != want {
		t.Fatalf("balance = %s, want %s (compensation must restore original balance)", got, want)
	}
}

func TestWithdrawToBank_InsufficientFundsNeverCallsBank(t *testing.T) {
	acc := ledger.NewAccount("wallet-1", money.FromRupees(10, 0))
	bank := &fakeBank{}

	err := WithdrawToBank(context.Background(), acc, bank, "idem-3", "1234567890", money.FromRupees(300, 0))
	if err == nil {
		t.Fatal("expected insufficient funds error")
	}
	if bank.paidOut {
		t.Fatal("bank must not be called when the local debit step fails")
	}
}
