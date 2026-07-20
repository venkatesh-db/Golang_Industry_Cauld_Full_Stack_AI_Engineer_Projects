package gateway

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"fintechgateway/internal/auth"
	"fintechgateway/internal/breaker"
	"fintechgateway/internal/cache"
	"fintechgateway/internal/loadbalancer"
	"fintechgateway/internal/ratelimit"
	"fintechgateway/internal/waf"
)

func backendAddress(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestGateway_ProxiesToHealthyBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello from backend"))
	}))
	defer backend.Close()

	pool, err := loadbalancer.NewPool(loadbalancer.RoundRobin, []*loadbalancer.Backend{
		loadbalancer.NewBackend("b1", backendAddress(t, backend), 1),
	})
	if err != nil {
		t.Fatal(err)
	}

	gw, err := New(Config{
		Routes:        []Route{{PathPrefix: "/api", Pool: pool}},
		WAFConfig:     waf.DefaultConfig(),
		BreakerConfig: breaker.DefaultConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/payments", nil)
	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "hello from backend" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestGateway_UnknownPathReturns404(t *testing.T) {
	pool, _ := loadbalancer.NewPool(loadbalancer.RoundRobin, []*loadbalancer.Backend{loadbalancer.NewBackend("b1", "127.0.0.1:1", 1)})
	gw, _ := New(Config{Routes: []Route{{PathPrefix: "/api", Pool: pool}}, WAFConfig: waf.DefaultConfig(), BreakerConfig: breaker.DefaultConfig()})

	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestGateway_RetriesAgainstHealthyBackendAfterFailure proves the retry
// path: one backend always 500s, the other always succeeds, and a
// least-connections pool routing to both must still return 200 by
// retrying onto the healthy one.
func TestGateway_RetriesAgainstHealthyBackendAfterFailure(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer healthy.Close()

	pool, _ := loadbalancer.NewPool(loadbalancer.RoundRobin, []*loadbalancer.Backend{
		loadbalancer.NewBackend("failing", backendAddress(t, failing), 1),
		loadbalancer.NewBackend("healthy", backendAddress(t, healthy), 1),
	})

	gw, _ := New(Config{
		Routes:        []Route{{PathPrefix: "/api", Pool: pool}},
		WAFConfig:     waf.DefaultConfig(),
		BreakerConfig: breaker.Config{FailureThreshold: 100, OpenDuration: time.Hour, HalfOpenSuccessThreshold: 1, MaxHalfOpenRequests: 1},
	})

	successes := 0
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		rec := httptest.NewRecorder()
		gw.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			successes++
		}
	}
	// Round-robin alternates failing/healthy; with 3 retry attempts per
	// request, every request should eventually land on `healthy`.
	if successes != 10 {
		t.Fatalf("successes = %d, want 10 (retry should route around the always-failing backend)", successes)
	}
}

// TestGateway_BreakerTripsAndStopsCallingDeadBackend proves the breaker
// is actually wired in: after enough consecutive failures the backend
// stops receiving requests at all (fails fast locally instead).
func TestGateway_BreakerTripsAndStopsCallingDeadBackend(t *testing.T) {
	var callCount int64
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&callCount, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dead.Close()

	pool, _ := loadbalancer.NewPool(loadbalancer.RoundRobin, []*loadbalancer.Backend{
		loadbalancer.NewBackend("dead", backendAddress(t, dead), 1),
	})

	gw, _ := New(Config{
		Routes:    []Route{{PathPrefix: "/api", Pool: pool}},
		WAFConfig: waf.DefaultConfig(),
		BreakerConfig: breaker.Config{
			FailureThreshold: 3, OpenDuration: time.Hour, HalfOpenSuccessThreshold: 1, MaxHalfOpenRequests: 1,
		},
	})

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		rec := httptest.NewRecorder()
		gw.Handler().ServeHTTP(rec, req)
	}

	// Only Pool exists with one backend and maxProxyAttempts=3 per
	// request, so a single request can call the backend up to 3 times
	// before the breaker trips mid-request; after FailureThreshold=3
	// failures the breaker opens and every subsequent attempt short
	// circuits without calling the backend at all.
	got := atomic.LoadInt64(&callCount)
	if got > 5 {
		t.Fatalf("backend was called %d times across 10 requests; breaker should have stopped calling it well before that", got)
	}
}

func TestGateway_CacheAvoidsSecondBackendCall(t *testing.T) {
	var callCount int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&callCount, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("cached-value"))
	}))
	defer backend.Close()

	pool, _ := loadbalancer.NewPool(loadbalancer.RoundRobin, []*loadbalancer.Backend{
		loadbalancer.NewBackend("b1", backendAddress(t, backend), 1),
	})

	gw, _ := New(Config{
		Routes:        []Route{{PathPrefix: "/api", Pool: pool, Cacheable: true}},
		Cache:         cache.New(100, time.Minute),
		WAFConfig:     waf.DefaultConfig(),
		BreakerConfig: breaker.DefaultConfig(),
	})

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/rates", nil)
		rec := httptest.NewRecorder()
		gw.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "cached-value" {
			t.Fatalf("request %d: status=%d body=%q", i, rec.Code, rec.Body.String())
		}
	}

	if got := atomic.LoadInt64(&callCount); got != 1 {
		t.Fatalf("backend was called %d times, want 1 (rest should be cache hits)", got)
	}
}

func TestGateway_RateLimitBlocksExcessTraffic(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	pool, _ := loadbalancer.NewPool(loadbalancer.RoundRobin, []*loadbalancer.Backend{
		loadbalancer.NewBackend("b1", backendAddress(t, backend), 1),
	})
	// Freeze the clock: with a fixed `now`, no tokens refill between
	// requests, so a burst of 2 followed by 5 back-to-back requests
	// deterministically yields 3x 429 — no dependence on wall-clock timing
	// or how slow the test host / -race build happens to be.
	frozen := time.Unix(0, 0)
	limiter := ratelimit.NewWithClock(1000, 2, func() time.Time { return frozen })
	defer limiter.Close()

	gw, _ := New(Config{
		Routes:        []Route{{PathPrefix: "/api", Pool: pool}},
		RateLimiter:   limiter,
		WAFConfig:     waf.DefaultConfig(),
		BreakerConfig: breaker.DefaultConfig(),
		ClientKey:     func(r *http.Request) string { return "same-caller" },
	})

	codes := make([]int, 5)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		rec := httptest.NewRecorder()
		gw.Handler().ServeHTTP(rec, req)
		codes[i] = rec.Code
	}

	tooMany := 0
	for _, c := range codes {
		if c == http.StatusTooManyRequests {
			tooMany++
		}
	}
	if tooMany == 0 {
		t.Fatalf("expected at least one 429 with burst=2 and 5 requests, got codes %v", codes)
	}
}

func TestGateway_RequireAuthRejectsMissingToken(t *testing.T) {
	pool, _ := loadbalancer.NewPool(loadbalancer.RoundRobin, []*loadbalancer.Backend{loadbalancer.NewBackend("b1", "127.0.0.1:1", 1)})
	verifier, _ := auth.NewVerifier([]byte("secret"))

	gw, _ := New(Config{
		Routes:        []Route{{PathPrefix: "/api", Pool: pool, RequireAuth: true}},
		Verifier:      verifier,
		WAFConfig:     waf.DefaultConfig(),
		BreakerConfig: breaker.DefaultConfig(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestGateway_WAFRejectsHostileRequestBeforeRoutingOrBackend(t *testing.T) {
	var called int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&called, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	pool, _ := loadbalancer.NewPool(loadbalancer.RoundRobin, []*loadbalancer.Backend{
		loadbalancer.NewBackend("b1", backendAddress(t, backend), 1),
	})
	gw, _ := New(Config{Routes: []Route{{PathPrefix: "/api", Pool: pool}}, WAFConfig: waf.DefaultConfig(), BreakerConfig: breaker.DefaultConfig()})

	req := httptest.NewRequest(http.MethodGet, "/api/x?id=1%27%20OR%20%271%27=%271", nil)
	rec := httptest.NewRecorder()
	gw.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if atomic.LoadInt64(&called) != 0 {
		t.Fatal("backend must never be called for a WAF-rejected request")
	}
}

func BenchmarkGateway_ProxySuccessPath(b *testing.B) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	u, _ := url.Parse(backend.URL)
	pool, _ := loadbalancer.NewPool(loadbalancer.RoundRobin, []*loadbalancer.Backend{loadbalancer.NewBackend("b1", u.Host, 1)})
	gw, _ := New(Config{Routes: []Route{{PathPrefix: "/api", Pool: pool}}, WAFConfig: waf.DefaultConfig(), BreakerConfig: breaker.DefaultConfig()})
	handler := gw.Handler()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/api/x?i="+strconv.Itoa(i), nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			i++
		}
	})
}
