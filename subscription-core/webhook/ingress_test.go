package webhook

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"subscriptioncore/cache"
	"subscriptioncore/domain"
	"subscriptioncore/provider/fake"
	"subscriptioncore/store/memory"
)

func newIngress() (*Ingress, *fake.Provider) {
	st := memory.New()
	fp := fake.New()
	proc := NewProcessor(fp, st, st, cache.NewMemory())
	return NewIngress(proc), fp
}

func post(t *testing.T, h http.Handler, sig string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhooks/provider", strings.NewReader("{}"))
	if sig != "" {
		req.Header.Set(SignatureHeader, sig)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIngress_ValidEventReturns200(t *testing.T) {
	in, fp := newIngress()
	fp.StageEvent("goodsig", domain.Event{
		ProviderEventID: "evt_1",
		Type:            domain.EventSubscriptionCreated,
		Subscription: domain.Subscription{
			ID: "s1", CustomerID: "u1", ProviderSubID: "psub", PlanID: "pro",
			Status: domain.StatusActive, ProviderUpdatedAt: time.Now(),
		},
	})

	rec := post(t, in, "goodsig")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != string(ResultProcessed) {
		t.Errorf("body = %q, want %q", rec.Body.String(), ResultProcessed)
	}
}

func TestIngress_DuplicateStillAcks200(t *testing.T) {
	in, fp := newIngress()
	fp.StageEvent("sig", domain.Event{
		ProviderEventID: "evt_dup",
		Type:            domain.EventSubscriptionCreated,
		Subscription:    domain.Subscription{ID: "s1", CustomerID: "u1", ProviderSubID: "psub", Status: domain.StatusActive, ProviderUpdatedAt: time.Now()},
	})
	_ = post(t, in, "sig")

	rec := post(t, in, "sig")
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != string(ResultDuplicate) {
		t.Errorf("body = %q, want duplicate", rec.Body.String())
	}
}

func TestIngress_BadSignatureReturns400(t *testing.T) {
	in, _ := newIngress()
	rec := post(t, in, "unknown-sig")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestIngress_NonPostReturns405(t *testing.T) {
	in, _ := newIngress()
	req := httptest.NewRequest(http.MethodGet, "/webhooks/provider", nil)
	rec := httptest.NewRecorder()
	in.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
