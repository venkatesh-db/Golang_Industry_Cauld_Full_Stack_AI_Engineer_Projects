package reconcile

import (
	"context"
	"testing"
	"time"

	"subscriptioncore/cache"
	"subscriptioncore/domain"
	"subscriptioncore/provider/fake"
	"subscriptioncore/store/memory"
)

func harness() (*Reconciler, *fake.Provider, *memory.Store, *cache.Memory) {
	prov := fake.New()
	st := memory.New()
	c := cache.NewMemory()
	return New(prov, st, c), prov, st, c
}

func sub(provID, subject, plan string, status domain.Status, updated time.Time) domain.Subscription {
	return domain.Subscription{
		ProviderSubID:     provID,
		CustomerID:        subject,
		PlanID:            plan,
		Status:            status,
		ProviderUpdatedAt: updated,
	}
}

func TestReconcileOne_AdoptsMissingLocal(t *testing.T) {
	r, prov, st, _ := harness()
	prov.SetSubscription(sub("sub_1", "user_1", "price_pro", domain.StatusActive, time.Now()))

	out, err := r.ReconcileOne(context.Background(), "sub_1")
	if err != nil {
		t.Fatal(err)
	}
	if out != OutcomeAdopted {
		t.Fatalf("out = %s, want adopted", out)
	}
	if _, err := st.GetByProviderID(context.Background(), "sub_1"); err != nil {
		t.Errorf("dropped-webhook subscription was not adopted locally: %v", err)
	}
}

func TestReconcileOne_RepairsDrift(t *testing.T) {
	r, prov, st, c := harness()
	t0 := time.Now()
	// Local says active; provider truth says canceled (a canceled webhook was dropped).
	_ = st.Upsert(context.Background(), sub("sub_1", "user_1", "price_pro", domain.StatusActive, t0))
	c.Set(context.Background(), "user_1", domain.Entitlement{Active: true})
	prov.SetSubscription(sub("sub_1", "user_1", "price_pro", domain.StatusCanceled, t0.Add(time.Minute)))

	out, err := r.ReconcileOne(context.Background(), "sub_1")
	if err != nil {
		t.Fatal(err)
	}
	if out != OutcomeRepaired {
		t.Fatalf("out = %s, want repaired", out)
	}
	got, _ := st.GetByProviderID(context.Background(), "sub_1")
	if got.Status != domain.StatusCanceled {
		t.Errorf("drift not repaired: status = %s, want canceled", got.Status)
	}
	if _, ok := c.Get(context.Background(), "user_1"); ok {
		t.Error("cache should be busted after repair")
	}
}

func TestReconcileOne_InSyncNoOp(t *testing.T) {
	r, prov, st, _ := harness()
	t0 := time.Now()
	s := sub("sub_1", "user_1", "price_pro", domain.StatusActive, t0)
	_ = st.Upsert(context.Background(), s)
	prov.SetSubscription(s)

	out, err := r.ReconcileOne(context.Background(), "sub_1")
	if err != nil {
		t.Fatal(err)
	}
	if out != OutcomeInSync {
		t.Errorf("out = %s, want in_sync", out)
	}
}

func TestReconcileOne_StaleProviderReadSkipped(t *testing.T) {
	r, prov, st, _ := harness()
	tNew := time.Now()
	// Local is newer (a fresh webhook already advanced it); provider read is older.
	_ = st.Upsert(context.Background(), sub("sub_1", "user_1", "price_pro", domain.StatusPastDue, tNew))
	prov.SetSubscription(sub("sub_1", "user_1", "price_pro", domain.StatusActive, tNew.Add(-time.Hour)))

	out, err := r.ReconcileOne(context.Background(), "sub_1")
	if err != nil {
		t.Fatal(err)
	}
	if out != OutcomeStale {
		t.Fatalf("out = %s, want stale_skip", out)
	}
	got, _ := st.GetByProviderID(context.Background(), "sub_1")
	if got.Status != domain.StatusPastDue {
		t.Errorf("stale provider read clobbered newer local state: status = %s", got.Status)
	}
}

func TestReconcileAll_MixedOutcomes(t *testing.T) {
	r, prov, st, _ := harness()
	t0 := time.Now()
	// sub_1: missing locally -> adopted. sub_2: in sync.
	prov.SetSubscription(sub("sub_1", "user_1", "price_pro", domain.StatusActive, t0))
	s2 := sub("sub_2", "user_2", "price_pro", domain.StatusActive, t0)
	_ = st.Upsert(context.Background(), s2)
	prov.SetSubscription(s2)

	out, err := r.ReconcileAll(context.Background(), []string{"sub_1", "sub_2"})
	if err != nil {
		t.Fatal(err)
	}
	if out["sub_1"] != OutcomeAdopted {
		t.Errorf("sub_1 = %s, want adopted", out["sub_1"])
	}
	if out["sub_2"] != OutcomeInSync {
		t.Errorf("sub_2 = %s, want in_sync", out["sub_2"])
	}
}
