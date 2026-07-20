// Command gateway wires every package into a runnable edge gateway
// fronting two fake backend services (payments, rates) and demonstrates
// the full request pipeline. Not a benchmark — see the *_test.go
// BenchmarkXxx functions in each package for measured latency/allocs.
package main

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"time"

	"fintechgateway/internal/auth"
	"fintechgateway/internal/breaker"
	"fintechgateway/internal/cache"
	"fintechgateway/internal/gateway"
	"fintechgateway/internal/loadbalancer"
	"fintechgateway/internal/ratelimit"
	"fintechgateway/internal/waf"
)

func fakeBackend(name string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"backend":%q,"path":%q}`, name, r.URL.Path)
	}))
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		log.Fatal(err)
	}
	return u.Host
}

func main() {
	payments1 := fakeBackend("payments-1")
	payments2 := fakeBackend("payments-2")
	rates := fakeBackend("rates-1")
	defer payments1.Close()
	defer payments2.Close()
	defer rates.Close()

	paymentsPool, err := loadbalancer.NewPool(loadbalancer.LeastConnections, []*loadbalancer.Backend{
		loadbalancer.NewBackend("payments-1", hostOf(payments1.URL), 1),
		loadbalancer.NewBackend("payments-2", hostOf(payments2.URL), 1),
	})
	if err != nil {
		log.Fatal(err)
	}
	ratesPool, err := loadbalancer.NewPool(loadbalancer.RoundRobin, []*loadbalancer.Backend{
		loadbalancer.NewBackend("rates-1", hostOf(rates.URL), 1),
	})
	if err != nil {
		log.Fatal(err)
	}

	healthChecker := loadbalancer.StartHealthChecker(
		append(paymentsPool.Backends(), ratesPool.Backends()...),
		5*time.Second,
		func(b *loadbalancer.Backend) bool { return true }, // demo: always healthy
	)
	defer healthChecker.Close()

	limiter := ratelimit.New(500, 50) // 500 req/s sustained, burst 50, per caller
	defer limiter.Close()

	rateCache := cache.New(1000, 30*time.Second)

	verifier, err := auth.NewVerifier([]byte("demo-signing-secret-do-not-use-in-prod"))
	if err != nil {
		log.Fatal(err)
	}

	gw, err := gateway.New(gateway.Config{
		Routes: []gateway.Route{
			{PathPrefix: "/api/payments", Pool: paymentsPool, RequireAuth: true},
			{PathPrefix: "/api/rates", Pool: ratesPool, Cacheable: true},
		},
		RateLimiter:   limiter,
		Verifier:      verifier,
		Cache:         rateCache,
		WAFConfig:     waf.DefaultConfig(),
		BreakerConfig: breaker.DefaultConfig(),
	})
	if err != nil {
		log.Fatal(err)
	}

	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           gw.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	fmt.Printf("gateway listening on %s\n", addr)
	fmt.Println("  GET  /api/rates/usd-inr        (cacheable, no auth)")
	fmt.Println("  GET  /api/payments/history      (requires Authorization: Bearer <token>)")
	log.Fatal(srv.ListenAndServe())
}
