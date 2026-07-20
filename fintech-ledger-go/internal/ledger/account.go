// Package ledger holds account balances and the single function allowed
// to move money between two accounts: Transfer.
package ledger

import (
	"errors"
	"sync"

	"fintechledger/internal/money"
)

var (
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrNegativeAmount    = errors.New("ledger: amount must not be negative")
)

type Account struct {
	ID string

	mu      sync.Mutex
	balance money.Paise
}

func NewAccount(id string, opening money.Paise) *Account {
	return &Account{ID: id, balance: opening}
}

func (a *Account) Balance() money.Paise {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.balance
}

// Debit and Credit lock internally; callers doing a single-account
// mutation use these. Transfer below needs to hold two locks at once, so
// it uses the unexported debit/credit that assume the caller already
// holds the lock.
func (a *Account) Debit(amt money.Paise) error {
	if amt.IsNegative() {
		return ErrNegativeAmount
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.debit(amt)
}

func (a *Account) Credit(amt money.Paise) error {
	if amt.IsNegative() {
		return ErrNegativeAmount
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.credit(amt)
	return nil
}

func (a *Account) debit(amt money.Paise) error {
	if a.balance.Sub(amt).IsNegative() {
		return ErrInsufficientFunds
	}
	a.balance = a.balance.Sub(amt)
	return nil
}

func (a *Account) credit(amt money.Paise) {
	a.balance = a.balance.Add(amt)
}

// Transfer moves amt from `from` to `to`.
//
// Locks are always acquired in ascending Account.ID order, regardless of
// transfer direction. A concurrent A→B transfer and B→A transfer both
// lock min(A,B) first — they never contend for the two locks in opposite
// order, which is the precondition for the classic ledger deadlock
// (goroutine 1 holds A waiting for B, goroutine 2 holds B waiting for A).
func Transfer(from, to *Account, amt money.Paise) error {
	if amt.IsNegative() {
		return ErrNegativeAmount
	}
	if from.ID == to.ID {
		return errors.New("ledger: cannot transfer to the same account")
	}
	first, second := from, to
	if second.ID < first.ID {
		first, second = second, first
	}
	first.mu.Lock()
	defer first.mu.Unlock()
	second.mu.Lock()
	defer second.mu.Unlock()

	if err := from.debit(amt); err != nil {
		return err
	}
	to.credit(amt)
	return nil
}
