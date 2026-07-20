package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	model "github.com/prometheus/client_model/go"
	"go.opentelemetry.io/otel/trace"
)

func TestWrapRecordsHTTPREDMetricsWithCanonicalLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newTestMetrics(t, registry)
	handler := metrics.Wrap("/v1/feed", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusCreated)
	}))

	request := httptest.NewRequest(http.MethodGet, "/v1/feed?viewer=123", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	requestCounter := metric(t, registry, "http_requests_total")
	if got := requestCounter.GetCounter().GetValue(); got != 1 {
		t.Fatalf("requests = %v, want 1", got)
	}
	if got := labels(requestCounter); got != "method=GET,route=/v1/feed,status=201" {
		t.Fatalf("request labels = %q", got)
	}

	duration := metric(t, registry, "http_request_duration_seconds")
	if got := duration.GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("duration samples = %d, want 1", got)
	}
	inFlight := metric(t, registry, "in_flight_requests")
	if got := inFlight.GetGauge().GetValue(); got != 0 {
		t.Fatalf("in flight = %v, want 0", got)
	}
}

func TestWrapGroupsUnexpectedRouteAndMethod(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newTestMetrics(t, registry)
	handler := metrics.Wrap("/v1/orders/991", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {}))

	request := httptest.NewRequest("BREW", "/v1/orders/991", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)

	requestCounter := metric(t, registry, "http_requests_total")
	if got := labels(requestCounter); got != "method=OTHER,route=unknown,status=200" {
		t.Fatalf("unexpected dynamic label was not grouped: %q", got)
	}
}

func TestWrapObservedReportsBoundedRequestOutcome(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newTestMetrics(t, registry)
	var observed HTTPObservation
	handler := metrics.WrapObserved("/v1/raw/123", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	}), func(_ context.Context, observation HTTPObservation) {
		observed = observation
	})

	request := httptest.NewRequest("CUSTOM", "/v1/raw/123", nil)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if observed.Route != unknownLabel || observed.Method != "OTHER" || observed.Status != http.StatusAccepted {
		t.Fatalf("observation = %#v, want bounded route/method and status 202", observed)
	}
	if observed.Duration <= 0 {
		t.Fatalf("observation duration = %s, want positive", observed.Duration)
	}
}

func TestDependencyMetricsUseConfiguredLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newTestMetrics(t, registry)
	metrics.ObserveDependency("postgres", "read_feed", DependencySuccess, 15*time.Millisecond)
	metrics.ObserveDependency("customer-991", "raw operation", DependencyStatus("error: customer 991"), 20*time.Millisecond)

	metricsFamily := metricFamily(t, registry, "dependency_duration_seconds")
	if len(metricsFamily.Metric) != 2 {
		t.Fatalf("dependency series = %d, want 2", len(metricsFamily.Metric))
	}
	got := []string{labels(metricsFamily.Metric[0]), labels(metricsFamily.Metric[1])}
	sort.Strings(got)
	want := []string{
		"dependency=postgres,operation=read_feed,status=success",
		"dependency=unknown,operation=unknown,status=unknown",
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("dependency labels = %#v, want %#v", got, want)
		}
	}
}

func TestStartDependencyRecordsOnlyOnce(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newTestMetrics(t, registry)
	done := metrics.StartDependency("postgres", "read_feed")
	done(DependencySuccess)
	done(DependencyError)

	if got := metric(t, registry, "dependency_duration_seconds").GetHistogram().GetSampleCount(); got != 1 {
		t.Fatalf("dependency samples = %d, want 1", got)
	}
}

func TestHTTPDurationIncludesTraceExemplar(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := newTestMetrics(t, registry)
	handler := metrics.Wrap("/v1/feed", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {}))
	traceID := trace.TraceID{1, 2, 3}
	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     trace.SpanID{4, 5, 6},
		TraceFlags: trace.FlagsSampled,
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/feed", nil)
	request = request.WithContext(trace.ContextWithSpanContext(request.Context(), spanContext))
	handler.ServeHTTP(httptest.NewRecorder(), request)

	histogram := metric(t, registry, "http_request_duration_seconds").GetHistogram()
	found := false
	for _, bucket := range histogram.Bucket {
		if bucket.Exemplar == nil {
			continue
		}
		for _, pair := range bucket.Exemplar.Label {
			if pair.GetName() == "trace_id" && pair.GetValue() == traceID.String() {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("HTTP duration histogram did not include a trace_id exemplar")
	}
}

func TestNewRequiresCanonicalRoutes(t *testing.T) {
	_, err := New(Config{Registerer: prometheus.NewRegistry(), Gatherer: prometheus.NewRegistry()})
	if err == nil {
		t.Fatal("New() error = nil, want a route allowlist error")
	}
}

func newTestMetrics(t *testing.T, registry *prometheus.Registry) *Metrics {
	t.Helper()
	metrics, err := New(Config{
		Routes:       []string{"/v1/feed"},
		Dependencies: map[string][]string{"postgres": {"read_feed"}},
		Registerer:   registry,
		Gatherer:     registry,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return metrics
}

func metric(t *testing.T, registry *prometheus.Registry, name string) *model.Metric {
	t.Helper()
	family := metricFamily(t, registry, name)
	if len(family.Metric) != 1 {
		t.Fatalf("%s series = %d, want 1", name, len(family.Metric))
	}
	return family.Metric[0]
}

func metricFamily(t *testing.T, registry *prometheus.Registry, name string) *model.MetricFamily {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	t.Fatalf("metric %q not found", name)
	return nil
}

func labels(metric *model.Metric) string {
	labels := make([]string, 0, len(metric.Label))
	for _, pair := range metric.Label {
		labels = append(labels, pair.GetName()+"="+pair.GetValue())
	}
	sort.Strings(labels)
	return join(labels)
}

func join(values []string) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += ","
		}
		result += value
	}
	return result
}
