// Command subscriptiond is the runnable reference wiring of the subscription
// core: HTTP webhook ingress + an entitlement-check endpoint, an in-process
// reconcile ticker (the pull leg), and graceful shutdown. It uses the in-memory
// adapters so it runs with zero external infrastructure — swap those for
// Postgres/Redis/Stripe behind the same ports for production.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"subscriptioncore/cache"
	"subscriptioncore/config"
	"subscriptioncore/domain"
	"subscriptioncore/entitlements"
	"subscriptioncore/provider/fake"
	"subscriptioncore/reconcile"
	"subscriptioncore/store/memory"
	"subscriptioncore/usage"
	"subscriptioncore/webhook"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("startup: %v", err)
	}

	// --- adapters (in-memory reference; swap behind the ports for prod) ---
	st := memory.New()
	st.SeedPlan(domain.Plan{
		ID:   "price_pro",
		Tier: "pro",
		Features: map[domain.Feature]int64{
			"api_calls": 10000, // metered cap
			"exports":   -1,    // unlimited
		},
		SeatIncluded: 3,
	})
	cch := cache.NewMemory()
	prov := fake.New()
	// The fake resolves events by signature token. Stage one demo event under
	// the configured secret so the running service is exercisable end-to-end:
	//   curl -XPOST -H "X-Signature: $WEBHOOK_SECRET" localhost:8080/webhooks/stripe
	// activates sub_1 for subject "user_1", after which /entitlement allows it.
	prov.StageEvent(cfg.WebhookSecret, domain.Event{
		ProviderEventID: "evt_demo_1",
		Type:            domain.EventSubscriptionCreated,
		Subscription: domain.Subscription{
			ProviderSubID:     "sub_1",
			CustomerID:        "user_1",
			PlanID:            "price_pro",
			Status:            domain.StatusActive,
			ProviderUpdatedAt: time.Now(),
		},
	})
	counter := usage.NewMemoryCounter()
	meter := usage.NewMeter(counter)
	flusher := usage.NewFlusher(counter, newLoggingLedger())

	proc := webhook.NewProcessor(prov, st, st, cch)
	ent := entitlements.New(st, st, cch, meter)
	rec := reconcile.New(prov, st, cch)

	// --- HTTP surface ---
	mux := http.NewServeMux()
	mux.Handle("/webhooks/stripe", webhook.NewIngress(proc))
	mux.HandleFunc("/entitlement", entitlementHandler(ent))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// --- lifecycle ---
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go runReconcileLoop(ctx, rec, cfg.ReconcileInterval)
	go func() {
		if err := flusher.Run(ctx, cfg.ReconcileInterval); err != nil {
			log.Printf("usage flusher stopped: %v", err)
		}
	}()

	go func() {
		log.Printf("subscriptiond listening on %s (reconcile every %s)", cfg.HTTPAddr, cfg.ReconcileInterval)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutdown signal received, draining...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Println("stopped")
}

// entitlementHandler answers "may this subject use this feature right now?".
// GET /entitlement?subject=user_1&feature=api_calls
func entitlementHandler(ent *entitlements.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subject := r.URL.Query().Get("subject")
		feature := r.URL.Query().Get("feature")
		if subject == "" || feature == "" {
			http.Error(w, "subject and feature query params are required", http.StatusBadRequest)
			return
		}
		d, err := ent.Check(r.Context(), subject, domain.Feature(feature))
		if err != nil {
			// Unknown subject is a client error, not a server fault.
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"allow":  d.Allow,
			"reason": d.Reason,
			"limit":  d.Limit,
		})
	}
}

// loggingLedger is the reference LedgerSink: it logs flushed usage instead of
// writing to Postgres. Swap for a pgx-backed usage_ledger writer in production.
type loggingLedger struct{}

func newLoggingLedger() *loggingLedger { return &loggingLedger{} }

func (loggingLedger) Append(_ context.Context, e usage.LedgerEntry) error {
	log.Printf("usage flush: subject=%s feature=%s period=%s delta=%d", e.Subject, e.Feature, e.Period, e.Amount)
	return nil
}

// runReconcileLoop runs the pull leg on a ticker until the context is canceled.
// The working set (active subscription ids) would come from the store in
// production; the reference build reconciles whatever the store currently holds.
func runReconcileLoop(ctx context.Context, rec *reconcile.Reconciler, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// The reference store does not expose an id listing, so this is a
			// placeholder hook; a real store would feed active ids here.
			_ = rec
			log.Println("reconcile tick (no working-set source wired in reference build)")
		}
	}
}
