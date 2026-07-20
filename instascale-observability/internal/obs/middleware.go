package obs

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// statusRecorder captures the response code for RED metrics/logs.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(c int) {
	r.code = c
	r.ResponseWriter.WriteHeader(c)
}

// Middleware wraps a handler with: inbound trace-context extraction, a server span,
// RED metrics (with a trace_id exemplar on the duration histogram), and a
// request-scoped logger carrying trace_id. route is the low-cardinality label
// (e.g. "/counts/{userId}"), not the raw path.
func (m *Metrics) Middleware(service, route string, next http.HandlerFunc) http.HandlerFunc {
	tracer := Tracer(service)
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(),
			propagation.HeaderCarrier(r.Header))
		ctx, span := tracer.Start(ctx, r.Method+" "+route, trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()

		traceID := TraceIDFromContext(ctx)
		reqLog := NewRequestLogger(service, route, r.Method, traceID)
		ctx = WithLogger(ctx, reqLog)

		m.InFlight.WithLabelValues(route).Inc()
		defer m.InFlight.WithLabelValues(route).Dec()

		rec := &statusRecorder{ResponseWriter: w, code: 200}
		start := time.Now()
		next(rec, r.WithContext(ctx))
		elapsed := time.Since(start).Seconds()

		code := strconv.Itoa(rec.code)
		m.Requests.WithLabelValues(route, r.Method, code).Inc()

		// Exemplar attaches trace_id to this latency sample -> Grafana metric->trace link.
		obsHist := m.Duration.WithLabelValues(route, r.Method)
		if ex, ok := obsHist.(prometheus.ExemplarObserver); ok && traceID != "" {
			ex.ObserveWithExemplar(elapsed, prometheus.Labels{"trace_id": traceID})
		} else {
			obsHist.Observe(elapsed)
		}

		reqLog.Info("request",
			"code", rec.code,
			"duration_ms", elapsed*1000,
			"path", r.URL.Path,
		)
	}
}
