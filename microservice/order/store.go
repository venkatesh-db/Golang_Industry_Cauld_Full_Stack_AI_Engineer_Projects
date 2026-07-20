package order

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Store provides the local consistency boundary. PostgreSQL implementations
// should use one database transaction for Transaction, Finalize, and Abandon.
type Store interface {
	Transaction(ctx context.Context, apply func(Tx) error) error
	ClaimDue(ctx context.Context, now time.Time, leaseFor time.Duration, limit int) ([]OutboxEvent, error)
	Finalize(ctx context.Context, eventID, leaseToken string, apply func(Tx, OutboxEvent) error) error
	Retry(ctx context.Context, eventID, leaseToken string, availableAt time.Time, reason string) error
	Abandon(ctx context.Context, eventID, leaseToken, reason string, apply func(Tx, OutboxEvent) error) error
}

type Tx interface {
	OrderByIdempotency(accountID, key string) (Order, bool)
	Order(id string) (Order, bool)
	PutOrder(Order) error
	Enqueue(orderID string, kind EventKind, availableAt time.Time) (OutboxEvent, error)
}

// MemoryStore is a serializable reference implementation. It is useful for
// tests; production code should back Store with PostgreSQL.
type MemoryStore struct {
	mu    sync.Mutex
	state memoryState
}

type memoryState struct {
	orders        map[string]Order
	byIdempotency map[string]string
	events        map[string]OutboxEvent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{state: memoryState{
		orders: make(map[string]Order), byIdempotency: make(map[string]string), events: make(map[string]OutboxEvent),
	}}
}

func (s memoryState) clone() memoryState {
	copyState := memoryState{orders: make(map[string]Order, len(s.orders)), byIdempotency: make(map[string]string, len(s.byIdempotency)), events: make(map[string]OutboxEvent, len(s.events))}
	for id, order := range s.orders {
		copyState.orders[id] = order
	}
	for key, id := range s.byIdempotency {
		copyState.byIdempotency[key] = id
	}
	for id, event := range s.events {
		copyState.events[id] = event
	}
	return copyState
}

type memoryTx struct{ state *memoryState }

func (s *MemoryStore) Transaction(ctx context.Context, apply func(Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transactionLocked(ctx, apply)
}

func (s *MemoryStore) transactionLocked(ctx context.Context, apply func(Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	next := s.state.clone()
	if err := apply(&memoryTx{state: &next}); err != nil {
		return err
	}
	s.state = next
	return nil
}

func (tx *memoryTx) OrderByIdempotency(accountID, key string) (Order, bool) {
	id, ok := tx.state.byIdempotency[idempotencyIndex(accountID, key)]
	if !ok {
		return Order{}, false
	}
	order, ok := tx.state.orders[id]
	return order, ok
}

func (tx *memoryTx) Order(id string) (Order, bool) {
	order, ok := tx.state.orders[id]
	return order, ok
}

func (tx *memoryTx) PutOrder(order Order) error {
	if _, exists := tx.state.orders[order.ID]; !exists {
		key := idempotencyIndex(order.AccountID, order.IdempotencyKey)
		if other, exists := tx.state.byIdempotency[key]; exists && other != order.ID {
			return ErrIdempotencyConflict
		}
		tx.state.byIdempotency[key] = order.ID
	}
	tx.state.orders[order.ID] = order
	return nil
}

func (tx *memoryTx) Enqueue(orderID string, kind EventKind, availableAt time.Time) (OutboxEvent, error) {
	id, err := newID()
	if err != nil {
		return OutboxEvent{}, err
	}
	event := OutboxEvent{ID: id, OrderID: orderID, Kind: kind, State: EventPending, AvailableAt: availableAt, CreatedAt: availableAt}
	tx.state.events[id] = event
	return event, nil
}

func (s *MemoryStore) ClaimDue(ctx context.Context, now time.Time, leaseFor time.Duration, limit int) ([]OutboxEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0)
	for id, event := range s.state.events {
		due := (event.State == EventPending || event.State == EventRetry) && !event.AvailableAt.After(now)
		expired := event.State == EventProcessing && !event.LeaseUntil.After(now)
		if due || expired {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		left, right := s.state.events[ids[i]], s.state.events[ids[j]]
		if left.AvailableAt.Equal(right.AvailableAt) {
			return left.ID < right.ID
		}
		return left.AvailableAt.Before(right.AvailableAt)
	})
	if len(ids) > limit {
		ids = ids[:limit]
	}
	claimed := make([]OutboxEvent, 0, len(ids))
	for _, id := range ids {
		token, err := newID()
		if err != nil {
			return nil, err
		}
		event := s.state.events[id]
		event.State, event.LeaseToken, event.LeaseUntil = EventProcessing, token, now.Add(leaseFor)
		s.state.events[id] = event
		claimed = append(claimed, event)
	}
	return claimed, nil
}

func (s *MemoryStore) Finalize(ctx context.Context, eventID, leaseToken string, apply func(Tx, OutboxEvent) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.state.events[eventID]
	if !ok || event.State != EventProcessing || event.LeaseToken != leaseToken {
		return ErrLeaseLost
	}
	return s.transactionLocked(ctx, func(tx Tx) error {
		if err := apply(tx, event); err != nil {
			return err
		}
		next := tx.(*memoryTx)
		current := next.state.events[eventID]
		current.State, current.LeaseToken, current.LeaseUntil, current.DeliveredAt = EventDelivered, "", time.Time{}, time.Now().UTC()
		next.state.events[eventID] = current
		return nil
	})
}

func (s *MemoryStore) Retry(ctx context.Context, eventID, leaseToken string, availableAt time.Time, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.state.events[eventID]
	if !ok || event.State != EventProcessing || event.LeaseToken != leaseToken {
		return ErrLeaseLost
	}
	event.State, event.LeaseToken, event.LeaseUntil = EventRetry, "", time.Time{}
	event.Attempts, event.AvailableAt, event.LastError = event.Attempts+1, availableAt, reason
	s.state.events[eventID] = event
	return nil
}

func (s *MemoryStore) Abandon(ctx context.Context, eventID, leaseToken, reason string, apply func(Tx, OutboxEvent) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.state.events[eventID]
	if !ok || event.State != EventProcessing || event.LeaseToken != leaseToken {
		return ErrLeaseLost
	}
	return s.transactionLocked(ctx, func(tx Tx) error {
		if err := apply(tx, event); err != nil {
			return err
		}
		next := tx.(*memoryTx)
		current := next.state.events[eventID]
		current.State, current.LeaseToken, current.LeaseUntil = EventDead, "", time.Time{}
		current.Attempts, current.LastError = current.Attempts+1, reason
		next.state.events[eventID] = current
		return nil
	})
}

// Snapshot is a diagnostic view for tests and operational adapters.
func (s *MemoryStore) Snapshot() ([]Order, []OutboxEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	orders := make([]Order, 0, len(s.state.orders))
	events := make([]OutboxEvent, 0, len(s.state.events))
	for _, order := range s.state.orders {
		orders = append(orders, order)
	}
	for _, event := range s.state.events {
		events = append(events, event)
	}
	sort.Slice(orders, func(i, j int) bool { return orders[i].ID < orders[j].ID })
	sort.Slice(events, func(i, j int) bool { return events[i].ID < events[j].ID })
	return orders, events
}
