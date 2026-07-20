package service

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/trace"

	"golangdebugging/diagnostics"
	"golangdebugging/failurelab"
	"golangdebugging/internal/observability"
	"golangdebugging/telemetry"
)

// Config defines one process's shared observability surface.
type Config struct {
	Name         string
	Routes       []string
	Dependencies map[string][]string
}

// Runtime bundles the common production concerns used by all demo services.
type Runtime struct {
	Name        string
	Logger      *slog.Logger
	Metrics     *telemetry.Metrics
	Traces      *observability.Runtime
	Diagnostics *diagnostics.Service

	diagnosticsToken string
	accessLogRatio   float64
}

// New builds logging, tracing, metrics, and incident snapshots for one service.
func New(ctx context.Context, config Config) (*Runtime, error) {
	traceRuntime, err := observability.New(ctx, observability.Config{
		ServiceName:   config.Name,
		Environment:   Env("DEPLOYMENT_ENVIRONMENT", "development"),
		Endpoint:      os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
		SamplingRatio: EnvFloat("TRACE_SAMPLING_RATIO", 1),
	})
	if err != nil {
		return nil, err
	}
	events := diagnostics.NewEventBuffer(250)
	baseHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	capturingHandler := diagnostics.CapturingHandler(baseHandler, events, slog.LevelError)
	logger := slog.New(observability.TraceHandler(capturingHandler)).With("service", config.Name)

	metrics, err := telemetry.New(telemetry.Config{
		Routes:       append(config.Routes, "/internal/failure-lab/{experiment}"),
		Dependencies: config.Dependencies,
	})
	if err != nil {
		_ = traceRuntime.Shutdown(ctx)
		return nil, err
	}
	accessLogRatio := EnvFloat("ACCESS_LOG_SAMPLE_RATIO", 0.01)
	if accessLogRatio < 0 {
		accessLogRatio = 0
	}
	if accessLogRatio > 1 {
		accessLogRatio = 1
	}
	return &Runtime{
		Name:             config.Name,
		Logger:           logger,
		Metrics:          metrics,
		Traces:           traceRuntime,
		Diagnostics:      diagnostics.NewService(diagnostics.Options{Events: events}),
		diagnosticsToken: os.Getenv("DIAGNOSTICS_TOKEN"),
		accessLogRatio:   accessLogRatio,
	}, nil
}

// Wrap applies metrics first and tracing second so logs emitted by the handler
// see the active server span while all outcomes, including panics, are counted.
func (r *Runtime) Wrap(route string, next http.Handler) http.Handler {
	return r.Traces.HTTPHandler(route, r.Metrics.WrapObserved(route, next, r.logRequest))
}

func (r *Runtime) logRequest(ctx context.Context, observation telemetry.HTTPObservation) {
	if observation.Status < http.StatusInternalServerError && !sampleTrace(ctx, r.accessLogRatio) {
		return
	}
	attributes := []any{
		"route", observation.Route,
		"method", observation.Method,
		"status", observation.Status,
		"duration_ms", float64(observation.Duration.Microseconds()) / 1000,
	}
	if observation.Status >= http.StatusInternalServerError {
		r.Logger.ErrorContext(ctx, "http request completed", attributes...)
		return
	}
	r.Logger.InfoContext(ctx, "http request completed", attributes...)
}

func sampleTrace(ctx context.Context, ratio float64) bool {
	if ratio <= 0 {
		return false
	}
	if ratio >= 1 {
		return true
	}
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return false
	}
	traceID := spanContext.TraceID()
	value := binary.BigEndian.Uint64(traceID[:8])
	return float64(value)/float64(^uint64(0)) < ratio
}

// MountOperations exposes internal metrics, snapshots, and failure experiments.
// These belong on a private listener in a real deployment.
func (r *Runtime) MountOperations(mux *http.ServeMux, lab *failurelab.Lab) {
	mux.Handle("GET /metrics", r.Metrics.Handler())
	mux.Handle("GET /internal/diagnostics/snapshot", diagnostics.NewHandler(diagnostics.HTTPOptions{
		Service:           r.Diagnostics,
		Authorize:         tokenAuthorizer(r.diagnosticsToken, "X-Diagnostics-Token"),
		AllowGoroutines:   true,
		RequestsPerMinute: 6,
		MaxTrackedClients: 1024,
	}))
	if lab != nil {
		handler := r.Wrap("/internal/failure-lab/{experiment}", lab.Handler())
		mux.Handle("GET /internal/failure-lab/{experiment}", handler)
		mux.Handle("POST /internal/failure-lab/{experiment}", handler)
	}
}

// Shutdown flushes spans within the supplied deadline.
func (r *Runtime) Shutdown(ctx context.Context) error {
	return r.Traces.Shutdown(ctx)
}

// ListenAndServe runs a hardened HTTP server and handles SIGINT/SIGTERM.
func (r *Runtime) ListenAndServe(address string, handler http.Handler) error {
	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	r.Logger.Info("service listening", "address", address)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Env returns a non-empty environment value or fallback.
func Env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// EnvInt parses an integer environment value or returns fallback.
func EnvInt(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

// EnvFloat parses a floating-point environment value or returns fallback.
func EnvFloat(name string, fallback float64) float64 {
	value, err := strconv.ParseFloat(os.Getenv(name), 64)
	if err != nil {
		return fallback
	}
	return value
}

// EnvBool parses a boolean environment value or returns fallback.
func EnvBool(name string, fallback bool) bool {
	value, err := strconv.ParseBool(os.Getenv(name))
	if err != nil {
		return fallback
	}
	return value
}

func tokenAuthorizer(expected, header string) diagnostics.Authorize {
	return func(request *http.Request) bool {
		if expected == "" {
			return false
		}
		return subtle.ConstantTimeCompare([]byte(request.Header.Get(header)), []byte(expected)) == 1
	}
}
