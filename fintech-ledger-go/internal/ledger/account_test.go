package ledger

import (
	"sync"
	"testing"
	"time"

	"fintechledger/internal/money"
)

// TestTransfer_NoDeadlockUnderOppositeDirections runs A→B and B→A
// transfers concurrently, hundreds of times, and fails the test if it
// doesn't finish within a timeout — a lock-ordering bug would hang here.
func TestTransfer_NoDeadlockUnderOppositeDirections(t *testing.T) {
	a := NewAccount("A", money.FromRupees(100000, 0))
	b := NewAccount("B", money.FromRupees(100000, 0))

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for i := 0; i < 500; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				_ = Transfer(a, b, money.FromRupees(1, 0))
			}()
			go func() {
				defer wg.Done()
				_ = Transfer(b, a, money.FromRupees(1, 0))
			}()
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Transfer deadlocked under concurrent opposite-direction transfers")
	}

	total := a.Balance().Add(b.Balance())
	want := money.FromRupees(200000, 0)
	if total != want {
		t.Fatalf("money was created or destroyed: total = %s, want %s", total, want)
	}
}

func TestTransfer_InsufficientFunds(t *testing.T) {
	a := NewAccount("A", money.FromRupees(10, 0))
	b := NewAccount("B", money.FromRupees(0, 0))

	if err := Transfer(a, b, money.FromRupees(100, 0)); err != ErrInsufficientFunds {
		t.Fatalf("got %v, want ErrInsufficientFunds", err)
	}
	if a.Balance() != money.FromRupees(10, 0) {
		t.Fatalf("failed transfer must not mutate balances, got %s", a.Balance())
	}
}

// TestTransfer_RejectsNegativeAmount guards against a negative amount
// silently reversing a transfer: debit(amt) treats a negative amt as an
// increase, so without this check Transfer(a, b, -100) would credit `a`
// and debit `b` while also bypassing `b`'s insufficient-funds check.
func TestTransfer_RejectsNegativeAmount(t *testing.T) {
	a := NewAccount("A", money.FromRupees(10, 0))
	b := NewAccount("B", money.FromRupees(10, 0))

	if err := Transfer(a, b, money.Paise(-500)); err != ErrNegativeAmount {
		t.Fatalf("got %v, want ErrNegativeAmount", err)
	}
	if a.Balance() != money.FromRupees(10, 0) || b.Balance() != money.FromRupees(10, 0) {
		t.Fatalf("rejected transfer must not mutate balances: a=%s b=%s", a.Balance(), b.Balance())
	}
}

func TestDebit_RejectsNegativeAmount(t *testing.T) {
	a := NewAccount("A", money.FromRupees(10, 0))
	if err := a.Debit(money.Paise(-500)); err != ErrNegativeAmount {
		t.Fatalf("got %v, want ErrNegativeAmount", err)
	}
	if a.Balance() != money.FromRupees(10, 0) {
		t.Fatalf("rejected debit must not mutate balance, got %s", a.Balance())
	}
}

func TestCredit_RejectsNegativeAmount(t *testing.T) {
	a := NewAccount("A", money.FromRupees(10, 0))
	if err := a.Credit(money.Paise(-500)); err != ErrNegativeAmount {
		t.Fatalf("got %v, want ErrNegativeAmount", err)
	}
	if a.Balance() != money.FromRupees(10, 0) {
		t.Fatalf("rejected credit must not mutate balance, got %s", a.Balance())
	}
}
