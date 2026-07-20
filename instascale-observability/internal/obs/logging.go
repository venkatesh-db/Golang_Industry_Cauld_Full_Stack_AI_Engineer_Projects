package obs

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey string

const loggerKey ctxKey = "obs.logger"

// NewLogger returns a JSON slog logger writing to stdout (picked up by promtail -> Loki).
// service is attached to every line; level parsed from LOG_LEVEL.
func NewLogger(service, level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(h).With("service", service)
}

// NewRequestLogger builds a per-request logger stamped with route/method and the
// active trace_id. That trace_id field is the pivot Grafana uses for Loki -> Tempo.
func NewRequestLogger(service, route, method, traceID string) *slog.Logger {
	l := slog.Default().With("service", service, "route", route, "method", method)
	if traceID != "" {
		l = l.With("trace_id", traceID)
	}
	return l
}

// WithLogger stores a request-scoped logger (already carrying trace_id) in context.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// LoggerFromContext returns the request-scoped logger, or the default if absent.
// Callers use this so every log line inside a request carries the same trace_id —
// which is the field Grafana uses to pivot Loki -> Tempo.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
