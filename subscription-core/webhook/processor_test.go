package webhook

import (
	"context"
	"testing"
	"time"

	"subscriptioncore/cache"
	"subscriptioncore/domain"
	"subscriptioncore/provider/fake"
	"subscriptioncore/store/memory"
)

func newProc() (*Processor, *fake.Provider, *memory.Store) {
	st := memory.New()
	fp := fake.New()
	return NewProcessor(fp, st, st, cache.NewMemory()), fp, st
}

func TestHandleCreatedThenDuplicate(t *testing.T) {
	p, fp, st := newProc()
	ctx := context.Background()
	t0 := time.Now()
	fp.StageEvent("sig1", domain.Event{
		ProviderEventID: "evt_1",
		Type:            domain.EventSubscriptionCreated,
		Subscription: domain.Subscription{
			ID: "s1", CustomerID: "u1", ProviderSubID: "psub", PlanID: "pro",
			Status: domain.StatusActive, ProviderUpdatedAt: t0,
		},
	})

	res, err := p.Handle(ctx, nil, "sig1")
	if err != nil || res != ResultProcessed {
		t.Fatalf("created: res=%s err=%v", res, err)
	}
	if sub, _ := st.GetBySubject(ctx, "u1"); sub.Status != domain.StatusActive {
		t.Fatalf("status = %s, want active", sub.Status)
	}

	// Redelivery of the same event id must be a no-op.
	res, err = p.Handle(ctx, nil, "sig1")
	if err != nil || res != ResultDuplicate {
		t.Fatalf("duplicate: res=%s err=%v", res, err)
	}
}

func TestHandleOutOfOrderIgnored(t *testing.T) {
	p, fp, st := newProc()
	ctx := context.Background()
	t1 := time.Now()
	t0 := t1.Add(-time.Hour)

	if err := st.Upsert(ctx, domain.Subscription{
		ID: "s1", CustomerID: "u1", ProviderSubID: "psub", PlanID: "pro",
		Status: domain.StatusActive, ProviderUpdatedAt: t1,
	}); err != nil {
		t.Fatal(err)
	}
	// An older cancel event arrives late; the watermark must reject it.
	fp.StageEvent("sigOld", domain.Event{
		ProviderEventID: "evt_old",
		Type:            domain.EventSubscriptionCanceled,
		Subscription:    domain.Subscription{ProviderSubID: "psub", CustomerID: "u1", ProviderUpdatedAt: t0},
	})

	res, err := p.Handle(ctx, nil, "sigOld")
	if err != nil || res != ResultStale {
		t.Fatalf("expected stale, res=%s err=%v", res, err)
	}
	if sub, _ := st.GetBySubject(ctx, "u1"); sub.Status != domain.StatusActive {
		t.Fatalf("stale event corrupted state: %s", sub.Status)
	}
}

func TestHandlePaymentFailedEntersGrace(t *testing.T) {
	p, fp, st := newProc()
	ctx := context.Background()
	t1 := time.Now()

	if err := st.Upsert(ctx, domain.Subscription{
		ID: "s1", CustomerID: "u1", ProviderSubID: "psub", PlanID: "pro",
		Status: domain.StatusActive, ProviderUpdatedAt: t1,
	}); err != nil {
		t.Fatal(err)
	}
	fp.StageEvent("sigPF", domain.Event{
		ProviderEventID: "evt_pf",
		Type:            domain.EventPaymentFailed,
		Subscription:    domain.Subscription{ProviderSubID: "psub", CustomerID: "u1", PlanID: "pro", ProviderUpdatedAt: t1.Add(time.Minute)},
	})

	res, err := p.Handle(ctx, nil, "sigPF")
	if err != nil || res != ResultProcessed {
		t.Fatalf("res=%s err=%v", res, err)
	}
	sub, _ := st.GetBySubject(ctx, "u1")
	if sub.Status != domain.StatusPastDue {
		t.Fatalf("want past_due, got %s", sub.Status)
	}
	if !domain.StatusGrantsAccess(sub.Status) {
		t.Fatal("past_due must retain access (grace), not hard cut off")
	}
}

func TestHandleIllegalTransitionRejected(t *testing.T) {
	p, fp, st := newProc()
	ctx := context.Background()
	t1 := time.Now()

	if err := st.Upsert(ctx, domain.Subscription{
		ID: "s1", CustomerID: "u1", ProviderSubID: "psub", PlanID: "pro",
		Status: domain.StatusCanceled, ProviderUpdatedAt: t1,
	}); err != nil {
		t.Fatal(err)
	}
	// payment.succeeded -> active, but canceled is terminal.
	fp.StageEvent("sigIll", domain.Event{
		ProviderEventID: "evt_ill",
		Type:            domain.EventPaymentSucceeded,
		Subscription:    domain.Subscription{ProviderSubID: "psub", CustomerID: "u1", ProviderUpdatedAt: t1.Add(time.Minute)},
	})

	res, err := p.Handle(ctx, nil, "sigIll")
	if err != nil || res != ResultIllegal {
		t.Fatalf("want illegal, res=%s err=%v", res, err)
	}
	if sub, _ := st.GetBySubject(ctx, "u1"); sub.Status != domain.StatusCanceled {
		t.Fatalf("illegal transition mutated state to %s", sub.Status)
	}
}

func TestHandleBadSignature(t *testing.T) {
	p, _, _ := newProc()
	if _, err := p.Handle(context.Background(), nil, "unknown-sig"); err == nil {
		t.Fatal("expected signature verification error")
	}
}
