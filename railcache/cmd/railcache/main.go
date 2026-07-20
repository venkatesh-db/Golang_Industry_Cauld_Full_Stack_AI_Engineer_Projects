// Command railcache runs the IRCTC-style train-search caching service.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"railcache/internal/cache"
	"railcache/internal/config"
	"railcache/internal/httpapi"
	"railcache/internal/metrics"
	"railcache/internal/search"
	"railcache/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// run wires and serves the application, returning an error instead of calling
// os.Exit, so that every deferred cleanup runs on the way out.
func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	rootCtx, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	// --- Postgres (source of truth) ---
	openCtx, cancelOpen := context.WithTimeout(rootCtx, 10*time.Second)
	db, err := store.Open(openCtx, cfg.DatabaseURL, store.Options{
		MaxConns:         cfg.DBMaxConns,
		StatementTimeout: cfg.DBStatementTimeout,
		ConnectTimeout:   cfg.DBConnectTimeout,
	})
	cancelOpen()
	if err != nil {
		return err
	}
	defer db.Close()

	// --- Redis + circuit breaker ---
	rc := cache.New(cfg.RedisAddr)
	defer rc.Close()
	var breakerOpen atomic.Bool
	breaker := cache.NewBreaker(rc, cfg.BreakerThreshold, cfg.BreakerCooldown, func(open bool) {
		breakerOpen.Store(open)
		log.Warn("redis circuit breaker state change", "open", open)
	})

	// --- Validation whitelist (station set), warmed now, refreshed in background ---
	stations := search.NewStationCache(db, log)
	warmCtx, cancelWarm := context.WithTimeout(rootCtx, 5*time.Second)
	if err := stations.Refresh(warmCtx); err != nil {
		log.Warn("initial station cache warm failed; will retry in background", "err", err)
	}
	cancelWarm()
	go stations.Run(rootCtx, cfg.StationRefresh)
	validator := search.NewValidator(stations, []string{"SL", "3A", "2A"}, cfg.DateWindowDays)

	// --- Metrics + gauges ---
	m := metrics.New()
	m.RegisterGauge("db_pool_acquired_conns", func() float64 { return float64(db.Stat().AcquiredConns()) })
	m.RegisterGauge("db_pool_total_conns", func() float64 { return float64(db.Stat().TotalConns()) })
	m.RegisterGauge("stations_known", func() float64 { return float64(stations.Len()) })
	m.RegisterGauge("redis_breaker_open", func() float64 {
		if breakerOpen.Load() {
			return 1
		}
		return 0
	})

	// --- Service (reads go through the breaker; readiness pings the raw client) ---
	svc := search.NewService(db, breaker, m, log, search.Params{
		TTL:                cfg.CacheTTL,
		TatkalTTL:          cfg.TatkalTTL,
		Jitter:             cfg.CacheJitter,
		NegativeTTL:        cfg.NegativeTTL,
		PhysicalMultiplier: cfg.PhysicalMultiplier,
		LockTTL:            cfg.LockTTL,
		WaitTries:          cfg.LockWaitTries,
		WaitEvery:          cfg.LockWaitEvery,
		FillTimeout:        cfg.FillTimeout,
	})

	public, internal, err := httpapi.NewServer(svc, validator, m, log, db, rc, cfg)
	if err != nil {
		return err
	}

	pubSrv := newHTTPServer(cfg.HTTPAddr, public)
	inSrv := newHTTPServer(cfg.AdminAddr, internal)

	errCh := make(chan error, 2)
	go serve(pubSrv, "public", cfg.HTTPAddr, log, errCh)
	go serve(inSrv, "internal", cfg.AdminAddr, log, errCh)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-stop:
		log.Info("shutdown signal received")
	case err := <-errCh:
		return err // a listener failed to bind/serve
	}

	// --- Graceful shutdown ---
	cancelRoot() // stop background station refresh
	shutCtx, cancelShut := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShut()
	_ = pubSrv.Shutdown(shutCtx)
	_ = inSrv.Shutdown(shutCtx)
	svc.Drain(shutCtx) // let in-flight background refreshes finish
	log.Info("shutdown complete")
	return nil
}

func newHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func serve(srv *http.Server, name, addr string, log *slog.Logger, errCh chan<- error) {
	log.Info("listening", "server", name, "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errCh <- err
	}
}
