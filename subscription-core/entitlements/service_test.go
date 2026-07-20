package entitlements

import (
	"context"
	"testing"
	"time"

	"subscriptioncore/cache"
	"subscriptioncore/domain"
	"subscriptioncore/store/memory"
	"subscriptioncore/usage"
)

func setup(t *testing.T) (*Service, *cache.Memory) {
	t.Helper()
	st := memory.New()
	st.SeedPlan(domain.Plan{
		ID:   "pro",
		Tier: "pro",
		Features: map[domain.Feature]int64{
			"api_calls": 10,
			"seats":     -1, // unlimited
		},
		SeatIncluded: 3,
	})
	if err := st.Upsert(context.Background(), domain.Subscription{
		ID: "sub_1", CustomerID: "u1", ProviderSubID: "psub_1",
		PlanID: "pro", Status: domain.StatusActive, ProviderUpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	c := cache.NewMemory()
	svc := New(st, st, c, usage.NewMeter(usage.NewMemoryCounter()))
	return svc, c
}

func TestCheckAllowed(t *testing.T) {
	svc, _ := setup(t)
	d, err := svc.Check(context.Background(), "u1", "api_calls")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allow || d.Reason != domain.ReasonAllowed {
		t.Fatalf("expected allowed, got %+v", d)
	}
}

func TestCheckUnlimitedSkipsUsage(t *testing.T) {
	svc, _ := setup(t)
	d, err := svc.Check(context.Background(), "u1", "seats")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allow || d.Limit != -1 {
		t.Fatalf("expected unlimited allow, got %+v", d)
	}
}

func TestCheckFailOpenWhenCacheDown(t *testing.T) {
	svc, c := setup(t)
	c.SetDown(true) // simulate Redis outage
	d, err := svc.Check(context.Background(), "u1", "api_calls")
	if err != nil {
		t.Fatalf("must fail open to DB, got err %v", err)
	}
	if !d.Allow {
		t.Fatal("expected allow on fail-open path")
	}
}

func TestCheckQuotaExceeded(t *testing.T) {
	st := memory.New()
	st.SeedPlan(domain.Plan{ID: "pro", Tier: "pro", Features: map[domain.Feature]int64{"api_calls": 2}})
	if err := st.Upsert(context.Background(), domain.Subscription{
		ID: "s", CustomerID: "u1", ProviderSubID: "p", PlanID: "pro", Status: domain.StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	meter := usage.NewMeter(usage.NewMemoryCounter())
	svc := New(st, st, cache.NewMemory(), meter)
	ctx := context.Background()

	if _, err := meter.Record(ctx, "u1", "api_calls", 2); err != nil { // hit the cap
		t.Fatal(err)
	}
	d, err := svc.Check(ctx, "u1", "api_calls")
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow || d.Reason != domain.ReasonQuotaExceeded {
		t.Fatalf("expected quota_exceeded, got %+v", d)
	}
}

func TestCheckCanceledNotActive(t *testing.T) {
	st := memory.New()
	st.SeedPlan(domain.Plan{ID: "pro", Tier: "pro", Features: map[domain.Feature]int64{"api_calls": 5}})
	if err := st.Upsert(context.Background(), domain.Subscription{
		ID: "s", CustomerID: "u1", ProviderSubID: "p", PlanID: "pro", Status: domain.StatusCanceled,
	}); err != nil {
		t.Fatal(err)
	}
	svc := New(st, st, cache.NewMemory(), usage.NewMeter(usage.NewMemoryCounter()))
	d, err := svc.Check(context.Background(), "u1", "api_calls")
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow || d.Reason != domain.ReasonNotActive {
		t.Fatalf("expected not_active, got %+v", d)
	}
}
