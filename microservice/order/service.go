package order

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

type PlaceOrderRequest struct {
	AccountID      string
	IdempotencyKey string
	Instrument     string
	Side           Side
	Quantity       int64
	LimitPrice     int64 // paise
}

type PlaceOrderResult struct {
	Order    Order
	Replayed bool
}

type OrderService struct {
	store Store
	now   func() time.Time
}

func NewOrderService(store Store, now func() time.Time) *OrderService {
	if now == nil {
		now = time.Now
	}
	return &OrderService{store: store, now: now}
}

func (s *OrderService) Place(ctx context.Context, request PlaceOrderRequest) (PlaceOrderResult, error) {
	if err := validate(request); err != nil {
		return PlaceOrderResult{}, err
	}
	fingerprint := requestFingerprint(request)
	var result PlaceOrderResult
	err := s.store.Transaction(ctx, func(tx Tx) error {
		if existing, ok := tx.OrderByIdempotency(request.AccountID, request.IdempotencyKey); ok {
			if existing.RequestHash != fingerprint {
				return ErrIdempotencyConflict
			}
			result = PlaceOrderResult{Order: existing, Replayed: true}
			return nil
		}
		id, err := newID()
		if err != nil {
			return err
		}
		now := s.now().UTC()
		order := Order{ID: id, AccountID: request.AccountID, IdempotencyKey: request.IdempotencyKey, RequestHash: fingerprint,
			Instrument: request.Instrument, Side: request.Side, Quantity: request.Quantity, LimitPrice: request.LimitPrice,
			Status: PendingMargin, Margin: MarginNone, CreatedAt: now, UpdatedAt: now}
		if err := tx.PutOrder(order); err != nil {
			return err
		}
		if _, err := tx.Enqueue(order.ID, ReserveMargin, now); err != nil {
			return err
		}
		result = PlaceOrderResult{Order: order}
		return nil
	})
	return result, err
}

func validate(r PlaceOrderRequest) error {
	if r.AccountID == "" {
		return invalid("account ID is required")
	}
	if r.IdempotencyKey == "" {
		return invalid("idempotency key is required")
	}
	if r.Instrument == "" {
		return invalid("instrument is required")
	}
	if r.Side != Buy && r.Side != Sell {
		return invalid("side must be BUY or SELL")
	}
	if r.Quantity <= 0 {
		return invalid("quantity must be positive")
	}
	if r.LimitPrice <= 0 {
		return invalid("limit price must be positive")
	}
	return nil
}

func requestFingerprint(r PlaceOrderRequest) string {
	value := fmt.Sprintf("%s|%s|%d|%d", r.Instrument, r.Side, r.Quantity, r.LimitPrice)
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
