package order

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type MarginGateway interface {
	Reserve(ctx context.Context, order Order, operationID string) (reservationID string, approved bool, reason string, err error)
	Release(ctx context.Context, reservationID, operationID string) error
}

type ExchangeGateway interface {
	Route(ctx context.Context, order Order, operationID string) (exchangeOrderID string, accepted bool, reason string, err error)
}

type RetryPolicy struct {
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

func (p RetryPolicy) delay(attempt int) time.Duration {
	if p.BaseBackoff <= 0 {
		return 0
	}
	delay := p.BaseBackoff
	for i := 1; i < attempt; i++ {
		if delay >= p.MaxBackoff/2 {
			return p.MaxBackoff
		}
		delay *= 2
	}
	if delay > p.MaxBackoff {
		return p.MaxBackoff
	}
	return delay
}

type Dispatcher struct {
	store    Store
	margin   MarginGateway
	exchange ExchangeGateway
	retry    RetryPolicy
	now      func() time.Time
	leaseFor time.Duration
}

func NewDispatcher(store Store, margin MarginGateway, exchange ExchangeGateway, retry RetryPolicy, now func() time.Time) *Dispatcher {
	if now == nil {
		now = time.Now
	}
	if retry.MaxAttempts <= 0 {
		retry.MaxAttempts = 8
	}
	if retry.MaxBackoff <= 0 {
		retry.MaxBackoff = 2 * time.Minute
	}
	if retry.BaseBackoff <= 0 {
		retry.BaseBackoff = time.Second
	}
	return &Dispatcher{store: store, margin: margin, exchange: exchange, retry: retry, now: now, leaseFor: 30 * time.Second}
}

// DispatchOnce processes a leased batch. A non-nil result means persistence or
// programming failure; expected remote failures are recorded for retry.
func (d *Dispatcher) DispatchOnce(ctx context.Context, limit int) (int, error) {
	now := d.now().UTC()
	events, err := d.store.ClaimDue(ctx, now, d.leaseFor, limit)
	if err != nil {
		return 0, err
	}
	for _, event := range events {
		if err := d.deliver(ctx, event); err != nil && !errors.Is(err, ErrLeaseLost) {
			return 0, err
		}
	}
	return len(events), nil
}

func (d *Dispatcher) deliver(ctx context.Context, event OutboxEvent) error {
	switch event.Kind {
	case ReserveMargin:
		return d.reserve(ctx, event)
	case RouteExchange:
		return d.route(ctx, event)
	case ReleaseMargin:
		return d.release(ctx, event)
	default:
		return d.abandon(ctx, event, fmt.Errorf("unknown event kind %q", event.Kind))
	}
}

func (d *Dispatcher) reserve(ctx context.Context, event OutboxEvent) error {
	order, err := d.orderFor(ctx, event)
	if err != nil {
		return err
	}
	reservationID, approved, reason, err := d.margin.Reserve(ctx, order, event.ID)
	if err != nil {
		return d.handleRemoteError(ctx, event, err)
	}
	return d.store.Finalize(ctx, event.ID, event.LeaseToken, func(tx Tx, _ OutboxEvent) error {
		current, ok := tx.Order(order.ID)
		if !ok {
			return ErrOrderNotFound
		}
		if !approved {
			current.Status, current.FailureReason, current.UpdatedAt = Rejected, reason, d.now().UTC()
			return tx.PutOrder(current)
		}
		current.Status, current.Margin, current.ReservationID, current.UpdatedAt = PendingRoute, MarginReserved, reservationID, d.now().UTC()
		if err := tx.PutOrder(current); err != nil {
			return err
		}
		_, err = tx.Enqueue(current.ID, RouteExchange, d.now().UTC())
		return err
	})
}

func (d *Dispatcher) route(ctx context.Context, event OutboxEvent) error {
	order, err := d.orderFor(ctx, event)
	if err != nil {
		return err
	}
	exchangeOrderID, accepted, reason, err := d.exchange.Route(ctx, order, event.ID)
	if err != nil {
		return d.handleRemoteError(ctx, event, err)
	}
	return d.store.Finalize(ctx, event.ID, event.LeaseToken, func(tx Tx, _ OutboxEvent) error {
		current, ok := tx.Order(order.ID)
		if !ok {
			return ErrOrderNotFound
		}
		if accepted {
			current.Status, current.ExchangeOrderID, current.UpdatedAt = Accepted, exchangeOrderID, d.now().UTC()
			return tx.PutOrder(current)
		}
		current.Status, current.Margin, current.FailureReason, current.UpdatedAt = Rejected, MarginReleasePending, reason, d.now().UTC()
		if err := tx.PutOrder(current); err != nil {
			return err
		}
		_, err = tx.Enqueue(current.ID, ReleaseMargin, d.now().UTC())
		return err
	})
}

func (d *Dispatcher) release(ctx context.Context, event OutboxEvent) error {
	order, err := d.orderFor(ctx, event)
	if err != nil {
		return err
	}
	if err := d.margin.Release(ctx, order.ReservationID, event.ID); err != nil {
		return d.handleRemoteError(ctx, event, err)
	}
	return d.store.Finalize(ctx, event.ID, event.LeaseToken, func(tx Tx, _ OutboxEvent) error {
		current, ok := tx.Order(order.ID)
		if !ok {
			return ErrOrderNotFound
		}
		current.Margin, current.UpdatedAt = MarginReleased, d.now().UTC()
		return tx.PutOrder(current)
	})
}

func (d *Dispatcher) orderFor(ctx context.Context, event OutboxEvent) (Order, error) {
	var order Order
	err := d.store.Transaction(ctx, func(tx Tx) error {
		var ok bool
		order, ok = tx.Order(event.OrderID)
		if !ok {
			return ErrOrderNotFound
		}
		return nil
	})
	return order, err
}

func (d *Dispatcher) handleRemoteError(ctx context.Context, event OutboxEvent, err error) error {
	if !isTransient(err) {
		return d.abandon(ctx, event, err)
	}
	if event.Attempts+1 >= d.retry.MaxAttempts {
		return d.abandon(ctx, event, err)
	}
	return d.store.Retry(ctx, event.ID, event.LeaseToken, d.now().UTC().Add(d.retry.delay(event.Attempts+1)), err.Error())
}

func (d *Dispatcher) abandon(ctx context.Context, event OutboxEvent, cause error) error {
	return d.store.Abandon(ctx, event.ID, event.LeaseToken, cause.Error(), func(tx Tx, _ OutboxEvent) error {
		current, ok := tx.Order(event.OrderID)
		if !ok {
			return ErrOrderNotFound
		}
		// The external outcome may be unknown. Do not compensate automatically.
		current.Status, current.FailureReason, current.UpdatedAt = ReconciliationRequired, cause.Error(), d.now().UTC()
		return tx.PutOrder(current)
	})
}
