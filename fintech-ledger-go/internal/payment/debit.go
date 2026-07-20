// Package payment exposes the customer-facing debit API.
package payment

import (
	"context"
	"errors"

	"fintechledger/internal/idempotency"
	"fintechledger/internal/ledger"
	"fintechledger/internal/money"
)

var (
	ErrMissingIdempotencyKey = errors.New("payment: Idempotency-Key header is required")
	ErrUnknownAccount        = errors.New("payment: unknown account")
)

type DebitRequest struct {
	IdempotencyKey string
	AccountID      string
	Amount         money.Paise
}

type DebitResponse struct {
	Status string
}

// Debit is safe against client retries: a client that times out waiting
// for a response and retries with the same Idempotency-Key replays the
// stored result instead of debiting a second time. Without this, a
// client retry after a timeout — unable to tell "the debit never
// happened" from "it happened but the response was lost in transit" — is
// the single most common cause of double-debit incidents in payment
// systems.
func Debit(ctx context.Context, store idempotency.Store, accounts map[string]*ledger.Account, req DebitRequest) (DebitResponse, error) {
	if req.IdempotencyKey == "" {
		return DebitResponse{}, ErrMissingIdempotencyKey
	}

	rec, found, err := store.Reserve(ctx, req.IdempotencyKey)
	if err != nil {
		return DebitResponse{}, err
	}
	if found {
		return DebitResponse{Status: string(rec.Body)}, nil
	}

	acc, ok := accounts[req.AccountID]
	if !ok {
		_ = store.Release(ctx, req.IdempotencyKey)
		return DebitResponse{}, ErrUnknownAccount
	}

	if err := acc.Debit(req.Amount); err != nil {
		_ = store.Release(ctx, req.IdempotencyKey)
		return DebitResponse{}, err
	}

	resp := DebitResponse{Status: "debited"}
	_ = store.Complete(ctx, req.IdempotencyKey, idempotency.Record{Key: req.IdempotencyKey, Body: []byte(resp.Status)})
	return resp, nil
}
