// Package cache is the Redis layer: cached search envelopes plus the
// distributed fill-lock that collapses the thundering herd.
package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrMiss signals the key was absent (a genuine cache miss).
var ErrMiss = errors.New("cache: miss")

// Store is the cache surface used by the search service. Keeping this small
// makes the cache-aside orchestration testable without a running Redis server.
type Store interface {
	GetWithTTL(ctx context.Context, key string) ([]byte, time.Duration, error)
	SetEx(ctx context.Context, key string, val []byte, ttl time.Duration) error
	Del(ctx context.Context, key string) error
	Acquire(ctx context.Context, key string, ttl time.Duration) (Lease, bool, error)
}

// Client wraps go-redis with the small surface RailCache needs.
type Client struct {
	rdb *redis.Client
}

// silenceRedisLogger routes go-redis's internal pool logger to nowhere. During
// a Redis outage it otherwise emits a line per failed dial attempt; our circuit
// breaker and structured request logs already capture that signal, so the
// library's chatter is pure noise (and a log-volume risk under a real outage).
func init() {
	redis.SetLogger(discardLogger{})
}

type discardLogger struct{}

func (discardLogger) Printf(context.Context, string, ...any) {}

// New builds a Redis client for addr.
//
// MaxRetries is -1 (no retries): a cache must fail fast. Retrying a cache read
// against a struggling Redis only deepens a pile-up — the correct response to a
// cache error is to fall back to the source of truth immediately, which the
// service layer does. Timeouts are deliberately tight for the same reason.
func New(addr string) *Client {
	return &Client{rdb: redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  1 * time.Second,
		ReadTimeout:  300 * time.Millisecond,
		WriteTimeout: 300 * time.Millisecond,
		PoolSize:     100,
		MinIdleConns: 10,
		MaxRetries:   -1,
	})}
}

// Close closes the underlying client.
func (c *Client) Close() error { return c.rdb.Close() }

// Ping checks connectivity (used by /healthz).
func (c *Client) Ping(ctx context.Context) error { return c.rdb.Ping(ctx).Err() }

// GetWithTTL returns the raw value and its remaining TTL in ONE round trip via a
// pipeline. Returns ErrMiss when absent. A transport error is returned verbatim
// so the caller can distinguish "miss" (fall through to fill) from "redis down"
// (fall back to Postgres).
func (c *Client) GetWithTTL(ctx context.Context, key string) ([]byte, time.Duration, error) {
	pipe := c.rdb.Pipeline()
	getCmd := pipe.Get(ctx, key)
	ttlCmd := pipe.PTTL(ctx, key)
	_, err := pipe.Exec(ctx)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, 0, ErrMiss
		}
		return nil, 0, fmt.Errorf("redis pipeline exec: %w", err) // transport error → caller treats as FALLBACK
	}
	val, err := getCmd.Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, 0, ErrMiss
		}
		return nil, 0, fmt.Errorf("redis get %q: %w", key, err)
	}
	return val, ttlCmd.Val(), nil
}

// SetEx stores a value with a TTL.
func (c *Client) SetEx(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, val, ttl).Err()
}

// Del removes a key (write-path invalidation).
func (c *Client) Del(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, key).Err()
}

// raw exposes the client for the lock helper in the same package.
func (c *Client) raw() *redis.Client { return c.rdb }
