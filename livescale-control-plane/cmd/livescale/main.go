// Command livescale runs the control-plane HTTP server: token verify, play
// authorize, and per-account concurrency via heartbeats. Single binary, no
// external services required (in-memory store by default).
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"livescale/internal/concurrency"
	"livescale/internal/config"
	"livescale/internal/httpx"
	"livescale/internal/obs"
	"livescale/internal/session"
)

func main() {
	cfg := config.FromEnv()
	metrics := obs.New()
	if err := cfg.Validate(); err != nil { // M2: fail fast on insecure config
		metrics.Log.Error("invalid config", "err", err)
		os.Exit(1)
	}
	mgr := concurrency.New(cfg.ShardCount)
	store := session.NewMemory(cfg.ShardCount)

	// M3: full timeout budget — protects the tail-latency goal against slow
	// clients (slowloris) on read, body, and write.
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpx.NewServer(cfg, mgr, store, metrics).Handler(),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Sweeper: reclaim expired sessions and decrement device counts. Coarse
	// ticker + lazy-expiry-on-read together keep concurrency counts honest.
	stopSweep := make(chan struct{})
	go sweep(cfg.SweepInterval, store, mgr, metrics, stopSweep)

	go func() {
		metrics.Log.Info("listening", "addr", cfg.Addr, "shards", cfg.ShardCount)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			metrics.Log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	metrics.Log.Info("shutting down")

	close(stopSweep)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		metrics.Log.Error("graceful shutdown failed", "err", err)
	}
}

func sweep(every time.Duration, store *session.Memory, mgr *concurrency.Manager, m *obs.Metrics, stop <-chan struct{}) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			now := time.Now().UnixNano()
			expired := store.ReapExpired(now)
			for _, s := range expired {
				mgr.Release(s.AccountID, s.DeviceID)
			}
			if n := mgr.ReapExpired(now); n > 0 || len(expired) > 0 {
				m.Reaped.Add(int64(len(expired) + n))
			}
		}
	}
}
