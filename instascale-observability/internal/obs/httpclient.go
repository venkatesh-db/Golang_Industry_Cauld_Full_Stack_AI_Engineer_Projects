package obs

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// ClientConfig captures the resilience knobs from ADR-004.
type ClientConfig struct {
	Timeout       time.Duration // per-attempt deadline
	MaxRetries    int           // bounded — the antidote to the retry-storm chaos
	FailThreshold int           // consecutive failures before the breaker opens
	OpenFor       time.Duration // how long the breaker stays open
}

// Client is a trace-propagating HTTP client with per-call deadlines, bounded
// jittered retries, and a circuit breaker. edge-api uses one per downstream so a
// storm trips the breaker instead of amplifying load without limit.
type Client struct {
	name   string
	http   *http.Client
	cfg    ClientConfig
	tracer trace.Tracer

	mu          sync.Mutex
	failures    int
	openedUntil time.Time
}

func NewClient(name string, cfg ClientConfig) *Client {
	return &Client{
		name:   name,
		http:   &http.Client{Timeout: cfg.Timeout},
		cfg:    cfg,
		tracer: Tracer("edge-api"),
	}
}

// ErrBreakerOpen is returned when the downstream circuit is open.
var ErrBreakerOpen = fmt.Errorf("circuit breaker open")

// Get performs an instrumented GET: it opens a client span, injects traceparent,
// enforces the breaker, and retries transient failures with jittered backoff.
func (c *Client) Get(ctx context.Context, url string) ([]byte, error) {
	if c.breakerOpen() {
		return nil, fmt.Errorf("%s: %w", c.name, ErrBreakerOpen)
	}

	ctx, span := c.tracer.Start(ctx, "GET "+c.name, trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			// Full jitter backoff caps amplification during a storm.
			backoff := time.Duration(rand.Int63n(int64(50*time.Millisecond) * int64(attempt)))
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		body, err := c.do(ctx, url)
		if err == nil {
			c.recordSuccess()
			return body, nil
		}
		lastErr = err
		c.recordFailure()
		if c.breakerOpen() {
			break
		}
	}
	span.RecordError(lastErr)
	return nil, fmt.Errorf("%s after %d attempts: %w", c.name, c.cfg.MaxRetries+1, lastErr)
}

func (c *Client) do(ctx context.Context, url string) ([]byte, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	otel.GetTextMapPropagator().Inject(attemptCtx, propagation.HeaderCarrier(req.Header))

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("downstream %d", resp.StatusCode)
	}
	return body, nil
}

func (c *Client) breakerOpen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Before(c.openedUntil)
}

func (c *Client) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	if c.failures >= c.cfg.FailThreshold {
		c.openedUntil = time.Now().Add(c.cfg.OpenFor)
		c.failures = 0
	}
}

func (c *Client) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures = 0
}
