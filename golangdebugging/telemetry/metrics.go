package telemetry

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/trace"
)

const unknownLabel = "unknown"

// DependencyStatus is a bounded outcome label for a downstream call.
// Do not derive it from an error message or an upstream request ID.
type DependencyStatus string

const (
	DependencySuccess  DependencyStatus = "success"
	DependencyError    DependencyStatus = "error"
	DependencyTimeout  DependencyStatus = "timeout"
	DependencyCanceled DependencyStatus = "canceled"
	DependencyRejected DependencyStatus = "rejected"
)

// Config controls the metric registry and, crucially, all permitted label
// values that could otherwise grow with request traffic.
type Config struct {
	// Routes is the finite set of canonical route templates, for example
	// "/v1/feed" and "/v1/users/{id}". Raw request paths are never used.
	Routes []string

	// Dependencies maps a finite downstream dependency name to its finite
	// operation names, for example {"postgres": {"read_feed", "write_post"}}.
	Dependencies map[string][]string

	// Registerer and Gatherer must point to the same Prometheus registry when
	// either is supplied. Both default to the global Prometheus registry.
	Registerer prometheus.Registerer
	Gatherer   prometheus.Gatherer

	// Namespace prefixes metric names. Leave empty to expose the standard names
	// http_requests_total and http_request_duration_seconds exactly as shown.
	Namespace string

	// DurationBuckets applies to HTTP and dependency histograms. Defaults are
	// appropriate for millisecond-to-second web and RPC calls.
	DurationBuckets []float64
}

// Metrics owns a bounded set of HTTP and dependency Prometheus metrics.
type Metrics struct {
	routes       map[string]struct{}
	dependencies map[string]map[string]struct{}

	requests           *prometheus.CounterVec
	requestDuration    *prometheus.HistogramVec
	inFlight           *prometheus.GaugeVec
	dependencyDuration *prometheus.HistogramVec
	handler            http.Handler
}

// HTTPObservation is the bounded result of one instrumented request. Route and
// Method contain their canonical metric values rather than raw request data.
type HTTPObservation struct {
	Route    string
	Method   string
	Status   int
	Duration time.Duration
}

// HTTPObserver receives a completed request after its metrics are recorded.
type HTTPObserver func(context.Context, HTTPObservation)

// New registers the requested metrics. A non-empty canonical route allowlist
// is required to prevent high-cardinality URL labels in production.
func New(config Config) (*Metrics, error) {
	routes, err := makeAllowlist(config.Routes, "route")
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, errors.New("telemetry: at least one canonical route is required")
	}
	dependencies, err := makeDependencyAllowlist(config.Dependencies)
	if err != nil {
		return nil, err
	}

	registerer := config.Registerer
	gatherer := config.Gatherer
	if registerer == nil && gatherer == nil {
		registerer = prometheus.DefaultRegisterer
		gatherer = prometheus.DefaultGatherer
	} else if registerer == nil || gatherer == nil {
		return nil, errors.New("telemetry: Registerer and Gatherer must be supplied together")
	}
	buckets := config.DurationBuckets
	if len(buckets) == 0 {
		buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	}

	metrics := &Metrics{
		routes:       routes,
		dependencies: dependencies,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: config.Namespace,
			Name:      "http_requests_total",
			Help:      "Total completed HTTP requests.",
		}, []string{"route", "method", "status"}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: config.Namespace,
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency in seconds.",
			Buckets:   buckets,
		}, []string{"route", "method", "status"}),
		inFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: config.Namespace,
			Name:      "in_flight_requests",
			Help:      "HTTP requests currently being served.",
		}, []string{"route"}),
		dependencyDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: config.Namespace,
			Name:      "dependency_duration_seconds",
			Help:      "Downstream dependency call latency in seconds.",
			Buckets:   buckets,
		}, []string{"dependency", "operation", "status"}),
	}

	for name, collector := range map[string]prometheus.Collector{
		"http_requests_total":           metrics.requests,
		"http_request_duration_seconds": metrics.requestDuration,
		"in_flight_requests":            metrics.inFlight,
		"dependency_duration_seconds":   metrics.dependencyDuration,
	} {
		if err := registerer.Register(collector); err != nil {
			return nil, fmt.Errorf("telemetry: register %s: %w", name, err)
		}
	}
	metrics.handler = promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
	return metrics, nil
}

// Handler exposes this metrics registry in Prometheus/OpenMetrics text format.
// Mount it on a dedicated internal port or ensure that its route is protected.
func (m *Metrics) Handler() http.Handler {
	return m.handler
}

// Wrap instruments a single, canonical HTTP route. The route must appear in
// Config.Routes; unknown routes are deliberately grouped under "unknown"
// rather than generating a new Prometheus time series.
func (m *Metrics) Wrap(route string, next http.Handler) http.Handler {
	return m.WrapObserved(route, next, nil)
}

// WrapObserved instruments a route and reports its bounded outcome to an
// optional observer. It lets callers implement sampled access logging without
// wrapping ResponseWriter a second time.
func (m *Metrics) WrapObserved(route string, next http.Handler, observer HTTPObserver) http.Handler {
	if next == nil {
		panic("telemetry: next HTTP handler is required")
	}
	route = m.routeLabel(route)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		method := requestMethod(request.Method)
		m.inFlight.WithLabelValues(route).Inc()
		defer m.inFlight.WithLabelValues(route).Dec()

		recorder := &responseRecorder{ResponseWriter: writer}
		completed := false
		defer func() {
			status := recorder.statusCode()
			if !completed {
				// A recovery middleware outside this handler will normally convert
				// the panic to an HTTP 500. Count the request as failed either way.
				status = http.StatusInternalServerError
			}
			statusLabel := strconv.Itoa(status)
			m.requests.WithLabelValues(route, method, statusLabel).Inc()
			duration := time.Since(started)
			observeWithTrace(request.Context(), m.requestDuration.WithLabelValues(route, method, statusLabel), duration.Seconds())
			if observer != nil {
				observer(request.Context(), HTTPObservation{Route: route, Method: method, Status: status, Duration: duration})
			}
		}()

		next.ServeHTTP(recorder, request)
		completed = true
	})
}

// ObserveDependency records one completed downstream call. Names and
// operations outside Config.Dependencies are grouped into "unknown" instead
// of creating high-cardinality metric series.
func (m *Metrics) ObserveDependency(dependency, operation string, status DependencyStatus, duration time.Duration) {
	m.ObserveDependencyContext(context.Background(), dependency, operation, status, duration)
}

// ObserveDependencyContext records a dependency call and attaches the active
// trace ID as a Prometheus exemplar. The trace ID is not a metric label and
// therefore does not increase time-series cardinality.
func (m *Metrics) ObserveDependencyContext(ctx context.Context, dependency, operation string, status DependencyStatus, duration time.Duration) {
	dependency, operation = m.dependencyLabels(dependency, operation)
	observeWithTrace(ctx, m.dependencyDuration.WithLabelValues(dependency, operation, dependencyStatusLabel(status)), duration.Seconds())
}

// StartDependency records the duration of one downstream call when the
// returned function is invoked. It fits naturally with defer:
//
//	done := metrics.StartDependency("postgres", "read_feed")
//	// make query
//	done(telemetry.DependencySuccess)
func (m *Metrics) StartDependency(dependency, operation string) func(DependencyStatus) {
	return m.StartDependencyContext(context.Background(), dependency, operation)
}

// StartDependencyContext times one dependency call and links its histogram
// observation to the active trace through an exemplar.
func (m *Metrics) StartDependencyContext(ctx context.Context, dependency, operation string) func(DependencyStatus) {
	started := time.Now()
	var once sync.Once
	return func(status DependencyStatus) {
		once.Do(func() {
			m.ObserveDependencyContext(ctx, dependency, operation, status, time.Since(started))
		})
	}
}

func observeWithTrace(ctx context.Context, observer prometheus.Observer, value float64) {
	spanContext := trace.SpanContextFromContext(ctx)
	if exemplarObserver, ok := observer.(prometheus.ExemplarObserver); ok && spanContext.IsValid() && spanContext.IsSampled() {
		exemplarObserver.ObserveWithExemplar(value, prometheus.Labels{"trace_id": spanContext.TraceID().String()})
		return
	}
	observer.Observe(value)
}

func (m *Metrics) routeLabel(route string) string {
	if _, ok := m.routes[route]; ok {
		return route
	}
	return unknownLabel
}

func (m *Metrics) dependencyLabels(dependency, operation string) (string, string) {
	operations, found := m.dependencies[dependency]
	if !found {
		return unknownLabel, unknownLabel
	}
	if _, found := operations[operation]; !found {
		return dependency, unknownLabel
	}
	return dependency, operation
}

func makeAllowlist(values []string, kind string) (map[string]struct{}, error) {
	allowed := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || value == unknownLabel {
			return nil, fmt.Errorf("telemetry: %s must be non-empty and cannot be %q", kind, unknownLabel)
		}
		if _, duplicate := allowed[value]; duplicate {
			return nil, fmt.Errorf("telemetry: duplicate %s %q", kind, value)
		}
		allowed[value] = struct{}{}
	}
	return allowed, nil
}

func makeDependencyAllowlist(dependencies map[string][]string) (map[string]map[string]struct{}, error) {
	allowed := make(map[string]map[string]struct{}, len(dependencies))
	for dependency, operations := range dependencies {
		if dependency == "" || dependency == unknownLabel {
			return nil, fmt.Errorf("telemetry: dependency must be non-empty and cannot be %q", unknownLabel)
		}
		operationSet, err := makeAllowlist(operations, "operation")
		if err != nil {
			return nil, fmt.Errorf("telemetry: dependency %q: %w", dependency, err)
		}
		if len(operationSet) == 0 {
			return nil, fmt.Errorf("telemetry: dependency %q has no operations", dependency)
		}
		allowed[dependency] = operationSet
	}
	return allowed, nil
}

func requestMethod(method string) string {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return strings.ToUpper(method)
	default:
		return "OTHER"
	}
}

func dependencyStatusLabel(status DependencyStatus) string {
	switch status {
	case DependencySuccess, DependencyError, DependencyTimeout, DependencyCanceled, DependencyRejected:
		return string(status)
	default:
		return unknownLabel
	}
}

type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *responseRecorder) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseRecorder) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *responseRecorder) ReadFrom(reader io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if readerFrom, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		return readerFrom.ReadFrom(reader)
	}
	return io.Copy(w.ResponseWriter, reader)
}

func (w *responseRecorder) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	if !w.wroteHeader {
		w.status = http.StatusSwitchingProtocols
		w.wroteHeader = true
	}
	return hijacker.Hijack()
}

func (w *responseRecorder) Push(target string, options *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, options)
}

func (w *responseRecorder) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *responseRecorder) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
