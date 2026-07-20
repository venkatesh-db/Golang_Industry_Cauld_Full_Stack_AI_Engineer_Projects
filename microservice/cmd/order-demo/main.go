// order-demo is a local operator console for the order placement saga.
// It deliberately uses in-process gateways so the workflow can be explored
// without exchange credentials or a running PostgreSQL instance.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/venkatesh/zerodha-order-orchestrator/order"
)

//go:embed static/index.html
var assets embed.FS

const (
	behaviorAccept  = "ACCEPT"
	behaviorReject  = "REJECT"
	behaviorTimeout = "TIMEOUT"
)

type demoMargin struct{}

func (demoMargin) Reserve(_ context.Context, order order.Order, _ string) (string, bool, string, error) {
	return "margin-" + order.ID[:8], true, "", nil
}

func (demoMargin) Release(_ context.Context, _ string, _ string) error { return nil }

// demoExchange consumes the selected behavior once. A timeout resets to
// acceptance, making the retry path visible when the next attempt is run.
type demoExchange struct {
	mu       sync.Mutex
	behavior string
}

func (g *demoExchange) SetBehavior(behavior string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.behavior = behavior
}

func (g *demoExchange) Behavior() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.behavior
}

func (g *demoExchange) Route(_ context.Context, placed order.Order, _ string) (string, bool, string, error) {
	g.mu.Lock()
	behavior := g.behavior
	g.behavior = behaviorAccept
	g.mu.Unlock()

	switch behavior {
	case behaviorReject:
		return "", false, "exchange rejected: price-band protection", nil
	case behaviorTimeout:
		return "", false, "", order.TransientError{Cause: errors.New("exchange gateway timed out; outcome is being retried")}
	default:
		return "EXCH-" + placed.ID[:8], true, "", nil
	}
}

type server struct {
	service    *order.OrderService
	dispatcher *order.Dispatcher
	store      *order.MemoryStore
	exchange   *demoExchange
}

func newServer() *server {
	store := order.NewMemoryStore()
	exchange := &demoExchange{behavior: behaviorAccept}
	return &server{
		service: order.NewOrderService(store, time.Now),
		dispatcher: order.NewDispatcher(store, demoMargin{}, exchange, order.RetryPolicy{
			MaxAttempts: 3, BaseBackoff: time.Second, MaxBackoff: 5 * time.Second,
		}, time.Now),
		store: store, exchange: exchange,
	}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/":
		http.ServeFileFS(w, r, assets, "static/index.html")
	case r.Method == http.MethodGet && r.URL.Path == "/api/state":
		s.writeState(w)
	case r.Method == http.MethodPost && r.URL.Path == "/api/orders":
		s.placeOrder(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/dispatch":
		s.dispatch(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/route-behavior":
		s.setRouteBehavior(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

type placeOrderInput struct {
	AccountID      string `json:"accountId"`
	IdempotencyKey string `json:"idempotencyKey"`
	Instrument     string `json:"instrument"`
	Side           string `json:"side"`
	Quantity       int64  `json:"quantity"`
	LimitPrice     int64  `json:"limitPrice"`
}

func (s *server) placeOrder(w http.ResponseWriter, r *http.Request) {
	var input placeOrderInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	result, err := s.service.Place(r.Context(), order.PlaceOrderRequest{
		AccountID: input.AccountID, IdempotencyKey: input.IdempotencyKey, Instrument: input.Instrument,
		Side: order.Side(input.Side), Quantity: input.Quantity, LimitPrice: input.LimitPrice,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, order.ErrIdempotencyConflict) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"order": result.Order, "replayed": result.Replayed})
}

func (s *server) dispatch(w http.ResponseWriter, r *http.Request) {
	count, err := s.dispatcher.DispatchOnce(r.Context(), 1)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	message := "No ready work. A retried event may still be in its back-off window."
	if count == 1 {
		message = "Processed one durable workflow event."
	}
	writeJSON(w, http.StatusOK, map[string]any{"processed": count, "message": message})
}

type behaviorInput struct {
	Behavior string `json:"behavior"`
}

func (s *server) setRouteBehavior(w http.ResponseWriter, r *http.Request) {
	var input behaviorInput
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	behavior := strings.ToUpper(input.Behavior)
	if behavior != behaviorAccept && behavior != behaviorReject && behavior != behaviorTimeout {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "behavior must be ACCEPT, REJECT, or TIMEOUT"})
		return
	}
	s.exchange.SetBehavior(behavior)
	s.writeState(w)
}

func (s *server) writeState(w http.ResponseWriter) {
	orders, events := s.store.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"orders": orders, "events": events, "nextExchangeBehavior": s.exchange.Behavior(),
	})
}

func decodeJSON(r *http.Request, output any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if decoder.More() {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func main() {
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "18090"
	}
	address := ":" + port
	log.Printf("Order orchestration demo listening on http://localhost%s", address)
	if err := http.ListenAndServe(address, newServer()); err != nil {
		log.Fatal(err)
	}
}
