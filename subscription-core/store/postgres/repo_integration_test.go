//go:build integration

// These tests spin up a real Postgres via testcontainers. They require a
// running Docker daemon and are excluded from the default build:
//
//	go test -tags=integration ./store/postgres/...
package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"subscriptioncore/domain"
	"subscriptioncore/store"
)

// startStore boots a throwaway Postgres, migrates it, and returns a ready Store.
func startStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("subs"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	st, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func sampleSub(status domain.Status, updated time.Time) domain.Subscription {
	return domain.Subscription{
		ID: "sub_1", CustomerID: "u1", ProviderSubID: "psub_1", PlanID: "pro",
		Status: status, SeatCount: 5,
		CurrentPeriodStart: updated, CurrentPeriodEnd: updated.Add(30 * 24 * time.Hour),
		TrialEnd: updated, ProviderUpdatedAt: updated,
	}
}

func TestSubscriptionUpsertAndGet(t *testing.T) {
	ctx := context.Background()
	st := startStore(t)

	in := sampleSub(domain.StatusActive, time.Now().UTC().Truncate(time.Second))
	if err := st.Upsert(ctx, in); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	byProv, err := st.GetByProviderID(ctx, "psub_1")
	if err != nil {
		t.Fatalf("get by provider id: %v", err)
	}
	if byProv.Status != domain.StatusActive || byProv.SeatCount != 5 {
		t.Fatalf("roundtrip mismatch: %+v", byProv)
	}

	bySubj, err := st.GetBySubject(ctx, "u1")
	if err != nil {
		t.Fatalf("get by subject: %v", err)
	}
	if bySubj.ProviderSubID != "psub_1" {
		t.Fatalf("get by subject mismatch: %+v", bySubj)
	}
}

func TestUpsertConflictUpdatesInPlace(t *testing.T) {
	ctx := context.Background()
	st := startStore(t)
	base := time.Now().UTC().Truncate(time.Second)

	if err := st.Upsert(ctx, sampleSub(domain.StatusActive, base)); err != nil {
		t.Fatal(err)
	}
	// Same provider_subscription_id, newer state -> update, not duplicate insert.
	next := sampleSub(domain.StatusPastDue, base.Add(time.Minute))
	if err := st.Upsert(ctx, next); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetByProviderID(ctx, "psub_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.StatusPastDue {
		t.Fatalf("status = %s, want past_due (conflict should update)", got.Status)
	}
}

func TestWebhookIdempotency(t *testing.T) {
	ctx := context.Background()
	st := startStore(t)

	first, err := st.MarkProcessed(ctx, "evt_1")
	if err != nil {
		t.Fatal(err)
	}
	if !first {
		t.Fatal("first MarkProcessed should report firstTime=true")
	}
	second, err := st.MarkProcessed(ctx, "evt_1")
	if err != nil {
		t.Fatal(err)
	}
	if second {
		t.Fatal("redelivered event must report firstTime=false (idempotency)")
	}
}

func TestPlanRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := startStore(t)

	want := domain.Plan{
		ID:   "pro",
		Tier: "pro",
		Features: map[domain.Feature]int64{
			"api_calls": 10,
			"seats":     -1,
		},
		SeatIncluded: 3,
	}
	if err := st.UpsertPlan(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, "pro")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tier != "pro" || got.SeatIncluded != 3 ||
		got.Features["api_calls"] != 10 || got.Features["seats"] != -1 {
		t.Fatalf("plan roundtrip mismatch: %+v", got)
	}
}

func TestGetNotFound(t *testing.T) {
	ctx := context.Background()
	st := startStore(t)

	if _, err := st.GetByProviderID(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want store.ErrNotFound", err)
	}
	if _, err := st.Get(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("plan err = %v, want store.ErrNotFound", err)
	}
}
