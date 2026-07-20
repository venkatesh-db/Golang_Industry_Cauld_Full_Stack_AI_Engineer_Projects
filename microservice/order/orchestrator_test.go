package order

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeMargin struct {
	reserveCalls int
	releaseCalls int
	reserveErr   []error
	approved     bool
	mu           sync.Mutex
}

func (f *fakeMargin) Reserve(_ context.Context, _ Order, _ string) (string, bool, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reserveCalls++
	if len(f.reserveErr) > 0 {
		err := f.reserveErr[0]
		f.reserveErr = f.reserveErr[1:]
		return "", false, "", err
	}
	return "reservation-1", f.approved, "insufficient margin", nil
}
func (f *fakeMargin) Release(_ context.Context, _ string, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseCalls++
	return nil
}

type fakeExchange struct {
	calls        int
	operationIDs []string
	results      []exchangeResult
}
type exchangeResult struct {
	id       string
	accepted bool
	reason   string
	err      error
}

func (f *fakeExchange) Route(_ context.Context, _ Order, operationID string) (string, bool, string, error) {
	f.calls++
	f.operationIDs = append(f.operationIDs, operationID)
	result := f.results[0]
	f.results = f.results[1:]
	return result.id, result.accepted, result.reason, result.err
}

func testRequest() PlaceOrderRequest {
	return PlaceOrderRequest{AccountID: "acct-7", IdempotencyKey: "client-814", Instrument: "NSE:INFY", Side: Buy, Quantity: 2, LimitPrice: 150012}
}

func TestPlaceIsIdempotentAndRejectsChangedReplay(t *testing.T) {
	store := NewMemoryStore()
	service := NewOrderService(store, func() time.Time { return time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC) })
	first, err := service.Place(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Place(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Replayed || first.Order.ID != second.Order.ID {
		t.Fatalf("expected replay of %s, got %#v", first.Order.ID, second)
	}
	changed := testRequest()
	changed.Quantity = 3
	if _, err := service.Place(context.Background(), changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("wanted idempotency conflict, got %v", err)
	}
	orders, events := store.Snapshot()
	if len(orders) != 1 || len(events) != 1 || events[0].Kind != ReserveMargin {
		t.Fatalf("atomic acceptance broken: orders=%d events=%#v", len(orders), events)
	}
}

func TestHappyPathTransitionsToAccepted(t *testing.T) {
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	service := NewOrderService(store, func() time.Time { return now })
	result, err := service.Place(context.Background(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	margin := &fakeMargin{approved: true}
	exchange := &fakeExchange{results: []exchangeResult{{id: "NSE-44", accepted: true}}}
	dispatcher := NewDispatcher(store, margin, exchange, RetryPolicy{MaxAttempts: 3}, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if _, err := dispatcher.DispatchOnce(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
	}
	orders, events := store.Snapshot()
	got := orders[0]
	if got.ID != result.Order.ID || got.Status != Accepted || got.Margin != MarginReserved || got.ExchangeOrderID != "NSE-44" {
		t.Fatalf("unexpected order: %#v", got)
	}
	if margin.reserveCalls != 1 || exchange.calls != 1 {
		t.Fatalf("expected one external call each: margin=%d exchange=%d", margin.reserveCalls, exchange.calls)
	}
	for _, event := range events {
		if event.State != EventDelivered {
			t.Fatalf("event %s was %s", event.Kind, event.State)
		}
	}
}

func TestDefinitiveExchangeRejectionCompensatesMargin(t *testing.T) {
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	service := NewOrderService(store, func() time.Time { return now })
	if _, err := service.Place(context.Background(), testRequest()); err != nil {
		t.Fatal(err)
	}
	margin := &fakeMargin{approved: true}
	exchange := &fakeExchange{results: []exchangeResult{{accepted: false, reason: "price band breach"}}}
	dispatcher := NewDispatcher(store, margin, exchange, RetryPolicy{MaxAttempts: 3}, func() time.Time { return now })
	for i := 0; i < 4; i++ {
		if _, err := dispatcher.DispatchOnce(context.Background(), 10); err != nil {
			t.Fatal(err)
		}
	}
	orders, _ := store.Snapshot()
	got := orders[0]
	if got.Status != Rejected || got.Margin != MarginReleased || got.FailureReason != "price band breach" {
		t.Fatalf("expected rejected order with released margin, got %#v", got)
	}
	if margin.releaseCalls != 1 {
		t.Fatalf("expected exactly one compensating release, got %d", margin.releaseCalls)
	}
}

func TestTransientExchangeFaultRetriesWithSameWorkflow(t *testing.T) {
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	service := NewOrderService(store, func() time.Time { return now })
	if _, err := service.Place(context.Background(), testRequest()); err != nil {
		t.Fatal(err)
	}
	margin := &fakeMargin{approved: true}
	exchange := &fakeExchange{results: []exchangeResult{{err: transient(errors.New("gateway timeout"))}, {id: "NSE-45", accepted: true}}}
	dispatcher := NewDispatcher(store, margin, exchange, RetryPolicy{MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: time.Minute}, func() time.Time { return now })
	if _, err := dispatcher.DispatchOnce(context.Background(), 10); err != nil {
		t.Fatal(err)
	} // reserve
	if _, err := dispatcher.DispatchOnce(context.Background(), 10); err != nil {
		t.Fatal(err)
	} // route retries
	now = now.Add(time.Second)
	if _, err := dispatcher.DispatchOnce(context.Background(), 10); err != nil {
		t.Fatal(err)
	} // route succeeds
	orders, events := store.Snapshot()
	if orders[0].Status != Accepted || exchange.calls != 2 {
		t.Fatalf("wanted accepted after one retry, order=%#v calls=%d", orders[0], exchange.calls)
	}
	if exchange.operationIDs[0] != exchange.operationIDs[1] {
		t.Fatalf("retry must reuse external idempotency key, got %#v", exchange.operationIDs)
	}
	for _, event := range events {
		if event.Kind == RouteExchange && event.Attempts != 1 {
			t.Fatalf("wanted one recorded retry, got %#v", event)
		}
	}
}

func TestExhaustedUnknownRouteOutcomeRequiresReconciliation(t *testing.T) {
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	service := NewOrderService(store, func() time.Time { return now })
	if _, err := service.Place(context.Background(), testRequest()); err != nil {
		t.Fatal(err)
	}
	margin := &fakeMargin{approved: true}
	exchange := &fakeExchange{results: []exchangeResult{{err: transient(errors.New("timeout"))}, {err: transient(errors.New("timeout"))}}}
	dispatcher := NewDispatcher(store, margin, exchange, RetryPolicy{MaxAttempts: 2, BaseBackoff: time.Second, MaxBackoff: time.Minute}, func() time.Time { return now })
	if _, err := dispatcher.DispatchOnce(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.DispatchOnce(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if _, err := dispatcher.DispatchOnce(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	orders, events := store.Snapshot()
	if orders[0].Status != ReconciliationRequired || orders[0].Margin != MarginReserved {
		t.Fatalf("ambiguous result must preserve reservation: %#v", orders[0])
	}
	for _, event := range events {
		if event.Kind == RouteExchange && event.State != EventDead {
			t.Fatalf("route event should be dead: %#v", event)
		}
	}
}
