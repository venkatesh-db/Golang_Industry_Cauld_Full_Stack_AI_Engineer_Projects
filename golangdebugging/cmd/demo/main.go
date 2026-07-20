// Command demo runs the diagnostics and telemetry packages in a small HTTP
// service. It is an integration example, not a replacement for wiring the
// packages into the application's own router and authentication.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golangdebugging/diagnostics"
	"golangdebugging/telemetry"
)

func main() {
	token := os.Getenv("DIAGNOSTICS_TOKEN")
	if token == "" {
		slog.Error("DIAGNOSTICS_TOKEN must be set")
		os.Exit(1)
	}
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = ":8080"
	}

	events := diagnostics.NewEventBuffer(250)
	logger := slog.New(diagnostics.CapturingHandler(
		slog.NewJSONHandler(os.Stdout, nil),
		events,
		slog.LevelError,
	))
	snapshots := diagnostics.NewService(diagnostics.Options{Events: events})
	metrics, err := telemetry.New(telemetry.Config{
		Routes: []string{"/healthz", "/v1/feed"},
		Dependencies: map[string][]string{
			"postgres": {"read_feed"},
		},
	})
	if err != nil {
		logger.Error("initialize metrics", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler())
	mux.Handle("GET /healthz", metrics.Wrap("/healthz", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})))
	mux.Handle("GET /v1/feed", metrics.Wrap("/v1/feed", feedHandler(logger, metrics)))
	mux.Handle("GET /internal/diagnostics/snapshot", diagnostics.NewHandler(diagnostics.HTTPOptions{
		Service:           snapshots,
		Authorize:         tokenAuthorizer(token),
		AllowGoroutines:   true,
		RequestsPerMinute: 6,
		MaxTrackedClients: 1024,
	}))

	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-shutdown
		context, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(context)
	}()

	logger.Info("demo service listening", "address", address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("demo service failed", "error", err)
		os.Exit(1)
	}
}

func feedHandler(logger *slog.Logger, metrics *telemetry.Metrics) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		done := metrics.StartDependency("postgres", "read_feed")
		if request.URL.Query().Get("fail") == "true" {
			done(telemetry.DependencyTimeout)
			logger.Error("feed dependency timed out", "dependency", "postgres", "trace_id", request.Header.Get("X-Trace-ID"))
			http.Error(writer, "feed temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		done(telemetry.DependencySuccess)
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"items": []string{"first-post", "second-post"},
		})
	})
}

func tokenAuthorizer(expected string) diagnostics.Authorize {
	return func(request *http.Request) bool {
		provided := request.Header.Get("X-Diagnostics-Token")
		return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
	}
}
