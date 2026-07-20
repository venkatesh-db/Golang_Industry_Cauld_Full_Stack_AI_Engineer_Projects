package observability

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestTraceHandlerAddsTraceAndSpanIDs(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(TraceHandler(slog.NewJSONHandler(&output, nil)))
	traceID := trace.TraceID{1, 2, 3}
	spanID := trace.SpanID{4, 5, 6}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}))

	logger.ErrorContext(ctx, "dependency failed")
	logLine := output.String()
	if !strings.Contains(logLine, traceID.String()) || !strings.Contains(logLine, spanID.String()) {
		t.Fatalf("trace-aware log = %s", logLine)
	}
}
