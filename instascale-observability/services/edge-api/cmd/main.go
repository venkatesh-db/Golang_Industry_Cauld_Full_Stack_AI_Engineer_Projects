// edge-api: the public BFF/gateway. Composes feed + counts via resilient,
// trace-propagating downstream clients (ADR-004).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"instascale/internal/chaos"
	"instascale/internal/obs"
	edge "instascale/services/edge-api/internal"
)

const service = "edge-api"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.SetDefault(obs.NewLogger(service, obs.Env("LOG_LEVEL", "info")))
	log := slog.Default()

	shutdownTracing, err := obs.InitTracing(ctx, service,
		obs.Env("OTEL_EXPORTER_OTLP_ENDPOINT", "otel-collector:4317"),
		obs.Env("OTEL_SERVICE_NAMESPACE", "instascale"))
	if err != nil {
		log.Error("init tracing", "err", err)
		os.Exit(1)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	metrics := obs.NewMetrics(service)
	ch := chaos.New(obs.EnvBool("CHAOS_ENABLED", false))

	clientCfg := obs.ClientConfig{
		Timeout:       obs.EnvMS("DOWNSTREAM_TIMEOUT_MS", 1000),
		MaxRetries:    obs.EnvInt("DOWNSTREAM_MAX_RETRIES", 2),
		FailThreshold: obs.EnvInt("BREAKER_FAIL_THRESHOLD", 5),
		OpenFor:       obs.EnvMS("BREAKER_OPEN_MS", 5000),
	}
	feedClient := obs.NewClient("feed-service", clientCfg)
	counterClient := obs.NewClient("counter-service", clientCfg)

	h := edge.NewHandler(feedClient, counterClient,
		obs.Env("FEED_SERVICE_URL", "http://feed-service:8081"),
		obs.Env("COUNTER_SERVICE_URL", "http://counter-service:8082"),
		ch)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /feed/{userId}", metrics.Middleware(service, "/feed/{userId}", h.GetFeed))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.Handle("GET /metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{EnableOpenMetrics: true}))
	ch.Register(mux)

	srv := &http.Server{Addr: obs.Env("EDGE_API_ADDR", ":8080"), Handler: mux}
	go func() {
		log.Info("edge-api listening", "addr", srv.Addr, "chaos", ch.Enabled())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}
