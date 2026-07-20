// feed-service: fan-out-on-read feed with Redis cache-aside over Postgres.
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
	feed "instascale/services/feed-service/internal"
)

const service = "feed-service"

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

	rdb := redis.NewClient(&redis.Options{Addr: obs.Env("REDIS_ADDR", "redis:6379")})
	defer rdb.Close()

	metrics := obs.NewMetrics(service)
	store := feed.NewStore(pool, rdb)
	ch := chaos.New(obs.EnvBool("CHAOS_ENABLED", false))
	h := feed.NewHandler(store, ch)

	go pollPoolStats(ctx, pool, metrics)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /feed/{userId}", metrics.Middleware(service, "/feed/{userId}", h.GetFeed))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", readyz(pool, rdb))
	mux.Handle("GET /metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{EnableOpenMetrics: true}))
	ch.Register(mux)

	srv := &http.Server{Addr: obs.Env("FEED_SERVICE_ADDR", ":8081"), Handler: mux}
	go func() {
		log.Info("feed-service listening", "addr", srv.Addr, "chaos", ch.Enabled())
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

func readyz(pool *pgxpool.Pool, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 250*time.Millisecond)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			http.Error(w, "postgres not ready", http.StatusServiceUnavailable)
			return
		}
		if err := rdb.Ping(ctx).Err(); err != nil {
			http.Error(w, "redis not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
