// counter-service: like/follower counters (Redis hot counters + Postgres source of
// truth), fully instrumented and chaos-enabled. First fully-wired service; its
// shape is the template for feed-service and edge-api.
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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"instascale/internal/chaos"
	"instascale/internal/obs"
	counter "instascale/services/counter-service/internal"
)

const service = "counter-service"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.SetDefault(obs.NewLogger(service, obs.Env("LOG_LEVEL", "info")))
	log := slog.Default()

	// --- tracing ---
	shutdownTracing, err := obs.InitTracing(ctx, service,
		obs.Env("OTEL_EXPORTER_OTLP_ENDPOINT", "otel-collector:4317"),
		obs.Env("OTEL_SERVICE_NAMESPACE", "instascale"))
	if err != nil {
		log.Error("init tracing", "err", err)
		os.Exit(1)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	// --- postgres pool (small max, so pool-exhaust chaos is demonstrable) ---
	poolCfg, err := pgxpool.ParseConfig(obs.Env("POSTGRES_DSN", ""))
	if err != nil {
		log.Error("parse dsn", "err", err)
		os.Exit(1)
	}
	poolCfg.MaxConns = int32(obs.EnvInt("DB_POOL_MAX_CONNS", 10))
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Error("pg connect", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	// --- redis ---
	rdb := redis.NewClient(&redis.Options{Addr: obs.Env("REDIS_ADDR", "redis:6379")})
	defer rdb.Close()

	// --- wiring ---
	metrics := obs.NewMetrics(service)
	store := counter.NewStore(pool, rdb)
	ch := chaos.New(obs.EnvBool("CHAOS_ENABLED", false))
	ch.AttachPool(store)
	h := counter.NewHandler(store, ch)

	// Continuously mirror pgx pool stats into Prometheus (pool-exhaust signature).
	go pollPoolStats(ctx, pool, metrics)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /counts/{userId}", metrics.Middleware(service, "/counts/{userId}", h.GetCounts))
	mux.HandleFunc("POST /counts/{userId}/like", metrics.Middleware(service, "/counts/{userId}/like", h.PostLike))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", readyz(pool, rdb))
	mux.Handle("GET /metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{EnableOpenMetrics: true}))
	ch.Register(mux) // chaos routes only if CHAOS_ENABLED

	srv := &http.Server{Addr: obs.Env("COUNTER_SERVICE_ADDR", ":8082"), Handler: mux}
	go func() {
		log.Info("counter-service listening", "addr", srv.Addr, "chaos", ch.Enabled())
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

// pollPoolStats mirrors pgxpool.Stat() into the pool gauges every second.
func pollPoolStats(ctx context.Context, pool *pgxpool.Pool, m *obs.Metrics) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			st := pool.Stat()
			m.DBPoolAcquired.Set(float64(st.AcquiredConns()))
			m.DBPoolIdle.Set(float64(st.IdleConns()))
			m.DBPoolMax.Set(float64(st.MaxConns()))
			m.DBPoolWaitCount.Set(float64(st.EmptyAcquireCount()))
		}
	}
}

// readyz degrades when a downstream dependency is unreachable — including when the
// pool is exhausted by chaos (acquire will time out).
func readyz(pool *pgxpool.Pool, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 250*time.Millisecond)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			http.Error(w, "postgres not ready: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		if err := rdb.Ping(ctx).Err(); err != nil {
			http.Error(w, "redis not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
