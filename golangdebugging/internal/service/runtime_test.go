package service

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestEnvironmentFallbacks(t *testing.T) {
	t.Setenv("TEST_INT", "not-an-integer")
	t.Setenv("TEST_BOOL", "true")
	if got := EnvInt("TEST_INT", 7); got != 7 {
		t.Fatalf("EnvInt() = %d, want 7", got)
	}
	if got := EnvBool("TEST_BOOL", false); !got {
		t.Fatal("EnvBool() = false, want true")
	}
}

func TestSampleTraceHonorsBounds(t *testing.T) {
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{1},
	})
	ctx := trace.ContextWithSpanContext(context.Background(), spanContext)
	if sampleTrace(ctx, 0) {
		t.Fatal("sampleTrace ratio 0 = true")
	}
	if !sampleTrace(ctx, 1) {
		t.Fatal("sampleTrace ratio 1 = false")
	}
	if sampleTrace(context.Background(), 0.5) {
		t.Fatal("sampleTrace without trace context = true")
	}
}
