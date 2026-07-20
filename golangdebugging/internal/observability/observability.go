package observability

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Config controls process-wide tracing. Endpoint is the complete OTLP/HTTP
// traces URL, for example http://otel-collector:4318/v1/traces.
type Config struct {
	ServiceName   string
	Environment   string
	Endpoint      string
	SamplingRatio float64
}

// StartDependencySpan starts a client span for a non-HTTP dependency such as
// PostgreSQL or Redis. HTTP clients are instrumented automatically.
func StartDependencySpan(ctx context.Context, dependency, operation string) (context.Context, trace.Span) {
	return otel.Tracer("golangdebugging/dependencies").Start(ctx, dependency+"."+operation,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("dependency.name", dependency),
			attribute.String("dependency.operation", operation),
		),
	)
}

// RecordError marks a dependency span as failed while keeping error text out of
// metrics labels.
func RecordError(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// Runtime owns the process tracer provider and trace-aware HTTP helpers.
type Runtime struct {
	provider *sdktrace.TracerProvider
}

// New configures W3C trace propagation and an optional OTLP exporter. A tracer
// provider is still installed without an endpoint so local runs retain trace
// IDs in logs without failing when an external collector is unavailable.
func New(ctx context.Context, config Config) (*Runtime, error) {
	if config.ServiceName == "" {
		return nil, errors.New("observability: service name is required")
	}
	if config.Environment == "" {
		config.Environment = "development"
	}
	if config.SamplingRatio <= 0 || config.SamplingRatio > 1 {
		config.SamplingRatio = 1
	}

	serviceResource, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", config.ServiceName),
			attribute.String("deployment.environment.name", config.Environment),
		),
	)
	if err != nil {
		return nil, err
	}
	options := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(serviceResource),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SamplingRatio))),
	}
	if config.Endpoint != "" {
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(config.Endpoint))
		if err != nil {
			return nil, err
		}
		options = append(options, sdktrace.WithBatcher(exporter))
	}
	provider := sdktrace.NewTracerProvider(options...)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return &Runtime{provider: provider}, nil
}

// Shutdown flushes any queued spans before the process exits.
func (r *Runtime) Shutdown(ctx context.Context) error {
	return r.provider.Shutdown(ctx)
}

// HTTPHandler creates a server span, extracts incoming W3C trace context, and
// returns the resulting trace ID to callers in X-Trace-ID.
func (r *Runtime) HTTPHandler(operation string, next http.Handler) http.Handler {
	return otelhttp.NewHandler(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if traceID := TraceID(request.Context()); traceID != "" {
			writer.Header().Set("X-Trace-ID", traceID)
		}
		next.ServeHTTP(writer, request)
	}), operation)
}

// HTTPClient returns a client that injects W3C trace context into downstream
// requests and records a child client span.
func (r *Runtime) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   timeout,
	}
}

// TraceHandler enriches structured logs with trace_id and span_id from ctx.
func TraceHandler(next slog.Handler) slog.Handler {
	if next == nil {
		panic("observability: next slog handler is required")
	}
	return &traceHandler{next: next}
}

// TraceID returns the active W3C trace ID or an empty string when there is no span.
func TraceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

type traceHandler struct {
	next slog.Handler
}

func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *traceHandler) Handle(ctx context.Context, record slog.Record) error {
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		record.AddAttrs(
			slog.String("trace_id", spanContext.TraceID().String()),
			slog.String("span_id", spanContext.SpanID().String()),
		)
	}
	return h.next.Handle(ctx, record)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{next: h.next.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{next: h.next.WithGroup(name)}
}
