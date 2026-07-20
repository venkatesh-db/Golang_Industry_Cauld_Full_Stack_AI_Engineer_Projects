// Package saga implements withdrawal-to-bank as a saga of compensable
// local steps instead of a two-phase commit spanning our ledger and the
// bank's system.
package saga

import (
	"context"
	"fmt"

	"fintechledger/internal/ledger"
	"fintechledger/internal/money"
)

type BankClient interface {
	Payout(ctx context.Context, idempotencyKey, bankAccountNo string, amt money.Paise) error
	ReversePayout(ctx context.Context, idempotencyKey string) error
}

type step struct {
	name       string
	do         func(ctx context.Context) error
	compensate func(ctx context.Context) error
}

// WithdrawToBank moves money from a wallet account to an external bank
// account (IMPS/NEFT rail) as a saga: each step commits locally and, if a
// later step fails, already-completed steps are undone by their
// compensation in reverse order.
//
// A 2PC coordinator would need to hold the wallet account locked for the
// full round trip to the bank rail — one slow bank response stalls every
// other operation on that account, and there's no participant on the
// bank's side willing to join our transaction coordinator anyway. The
// saga instead commits the wallet debit immediately (the account is only
// locked for that single local step) and issues a compensating credit if
// the bank leg fails.
func WithdrawToBank(ctx context.Context, acc *ledger.Account, bank BankClient, idempotencyKey, bankAccountNo string, amt money.Paise) error {
	steps := []step{
		{
			name: "debit_wallet",
			do:   func(ctx context.Context) error { return acc.Debit(amt) },
			compensate: func(ctx context.Context) error {
				return acc.Credit(amt)
			},
		},
		{
			name: "payout_to_bank",
			do:   func(ctx context.Context) error { return bank.Payout(ctx, idempotencyKey, bankAccountNo, amt) },
			compensate: func(ctx context.Context) error {
				return bank.ReversePayout(ctx, idempotencyKey)
			},
		},
	}

	completed := make([]step, 0, len(steps))
	for _, s := range steps {
		if err := s.do(ctx); err != nil {
			for i := len(completed) - 1; i >= 0; i-- {
				if cErr := completed[i].compensate(ctx); cErr != nil {
					return fmt.Errorf("saga: step %q failed (%w), and compensating %q also failed: %v", s.name, err, completed[i].name, cErr)
				}
			}
			return fmt.Errorf("saga: step %q failed and was rolled back: %w", s.name, err)
		}
		completed = append(completed, s)
	}
	return nil
}
