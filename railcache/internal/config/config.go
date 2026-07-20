// Package config loads RailCache runtime configuration from the environment.
//
// Every field has a production-sane default so the service boots against the
// local docker-compose stack with zero required env vars, but each is meant to
// be tuned per environment. Parsing is strict: a malformed value is a fatal
// startup error, never a silently-truncated default (a typo in a limit is a
// production incident waiting to happen).
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all tunables.
type Config struct {
	// Listeners. The public listener serves user traffic; the internal listener
	// serves admin/debug/metrics/pprof and must never be exposed publicly.
	HTTPAddr  string
	AdminAddr string

	DatabaseURL string
	RedisAddr   string

	// Freshness. RailCache uses logical expiry: a value is "fresh" until
	// FreshUntil (a timestamp inside the envelope), but physically survives in
	// Redis for LogicalTTL*PhysicalMultiplier so it can be served stale while a
	// single background refresh runs (stale-while-revalidate).
	CacheTTL           time.Duration // base logical TTL for a populated result
	TatkalTTL          time.Duration // logical TTL for near-date/high-churn queries
	CacheJitter        float64       // +/- fraction of logical TTL applied per write
	NegativeTTL        time.Duration // logical TTL for empty (negative) results
	PhysicalMultiplier int           // physical TTL = logical TTL * this (>=1)

	// Fill coordination.
	LockTTL       time.Duration // distributed fill-lock lifetime
	LockWaitTries int           // loser re-read attempts before coalescing to DB
	LockWaitEvery time.Duration // delay between loser re-read attempts
	FillTimeout   time.Duration // deadline for a (detached) DB fill/refresh

	// Circuit breaker around Redis: after N consecutive transport errors, skip
	// Redis for Cooldown so outage-mode requests fall back to Postgres in ~0ms.
	BreakerThreshold int
	BreakerCooldown  time.Duration

	// Postgres guardrails.
	DBMaxConns         int32
	DBStatementTimeout time.Duration // server-side kill for a slow fill query
	DBConnectTimeout   time.Duration

	// Rate limiting. Note: these limiters are per-process; the effective global
	// DB-protection budget is RateGlobal * replica_count (see ADR-002).
	RatePerIP       float64
	RatePerIPBurst  int
	RateGlobal      float64
	RateGlobalBurst int

	RequestTimeout time.Duration // per-request deadline enforced by middleware

	// Validation.
	DateWindowDays int           // reject travel dates further out than this
	StationRefresh time.Duration // how often to reload the station whitelist

	// AdminToken guards the internal admin routes. Empty => admin routes are
	// disabled entirely (fail closed).
	AdminToken string

	// HerdMaxN / HerdMaxConcurrency bound the built-in load generator so it can
	// never be turned into a self-DoS.
	HerdMaxN           int
	HerdMaxConcurrency int
}

// Load reads the environment and applies defaults, then validates invariants.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:           env("HTTP_ADDR", ":8080"),
		AdminAddr:          env("ADMIN_ADDR", ":9090"),
		DatabaseURL:        env("DATABASE_URL", "postgres://railcache:railcache@localhost:5433/railcache?sslmode=disable"),
		RedisAddr:          env("REDIS_ADDR", "localhost:6380"),
		CacheTTL:           envDur("CACHE_TTL", 45*time.Second),
		TatkalTTL:          envDur("TATKAL_TTL", 4*time.Second),
		CacheJitter:        envFloat("CACHE_TTL_JITTER", 0.2),
		NegativeTTL:        envDur("NEG_TTL", 10*time.Second),
		PhysicalMultiplier: envInt("PHYSICAL_TTL_MULTIPLIER", 10),
		LockTTL:            envDur("LOCK_TTL", 5*time.Second),
		LockWaitTries:      envInt("LOCK_WAIT_TRIES", 12),
		LockWaitEvery:      envDur("LOCK_WAIT_EVERY", 50*time.Millisecond),
		FillTimeout:        envDur("FILL_TIMEOUT", 3*time.Second),
		BreakerThreshold:   envInt("BREAKER_THRESHOLD", 5),
		BreakerCooldown:    envDur("BREAKER_COOLDOWN", 5*time.Second),
		DBMaxConns:         int32(envInt("DB_MAX_CONNS", 20)),
		DBStatementTimeout: envDur("DB_STATEMENT_TIMEOUT", 2*time.Second),
		DBConnectTimeout:   envDur("DB_CONNECT_TIMEOUT", 3*time.Second),
		RatePerIP:          envFloat("RATE_PER_IP", 50),
		RatePerIPBurst:     envInt("RATE_PER_IP_BURST", 100),
		RateGlobal:         envFloat("RATE_GLOBAL", 2000),
		RateGlobalBurst:    envInt("RATE_GLOBAL_BURST", 4000),
		RequestTimeout:     envDur("REQUEST_TIMEOUT", 5*time.Second),
		DateWindowDays:     envInt("DATE_WINDOW_DAYS", 120),
		StationRefresh:     envDur("STATION_REFRESH", 5*time.Minute),
		AdminToken:         env("ADMIN_TOKEN", ""),
		HerdMaxN:           envInt("HERD_MAX_N", 20000),
		HerdMaxConcurrency: envInt("HERD_MAX_CONCURRENCY", 500),
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// AdminEnabled reports whether admin routes should be mounted.
func (c Config) AdminEnabled() bool { return c.AdminToken != "" }

func (c Config) validate() error {
	if c.CacheJitter < 0 || c.CacheJitter >= 1 {
		return fmt.Errorf("CACHE_TTL_JITTER must be in [0,1), got %v", c.CacheJitter)
	}
	if c.PhysicalMultiplier < 1 {
		return fmt.Errorf("PHYSICAL_TTL_MULTIPLIER must be >= 1, got %d", c.PhysicalMultiplier)
	}
	for name, d := range map[string]time.Duration{
		"CACHE_TTL": c.CacheTTL, "TATKAL_TTL": c.TatkalTTL, "NEG_TTL": c.NegativeTTL,
		"LOCK_TTL": c.LockTTL, "FILL_TIMEOUT": c.FillTimeout, "REQUEST_TIMEOUT": c.RequestTimeout,
	} {
		if d <= 0 {
			return fmt.Errorf("%s must be > 0, got %v", name, d)
		}
	}
	// A fill must be able to finish inside the lock's lifetime; otherwise the
	// lock expires mid-fill and duplicate fillers race (still correct for
	// idempotent reads, but wasteful — see ADR-002).
	if c.FillTimeout > c.LockTTL {
		return fmt.Errorf("FILL_TIMEOUT (%v) should be <= LOCK_TTL (%v)", c.FillTimeout, c.LockTTL)
	}
	if c.DBStatementTimeout <= 0 {
		return fmt.Errorf("DB_STATEMENT_TIMEOUT must be > 0, got %v", c.DBStatementTimeout)
	}
	if c.HerdMaxN < 1 || c.HerdMaxConcurrency < 1 {
		return fmt.Errorf("HERD_MAX_N and HERD_MAX_CONCURRENCY must be >= 1")
	}
	return nil
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

// envDur parses a Go duration; a malformed value is fatal, not silently ignored.
func envDur(k string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(k)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		fatalf("%s: invalid duration %q: %v", k, v, err)
	}
	return d
}

func envInt(k string, def int) int {
	v, ok := os.LookupEnv(k)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fatalf("%s: invalid integer %q: %v", k, v, err)
	}
	return n
}

func envFloat(k string, def float64) float64 {
	v, ok := os.LookupEnv(k)
	if !ok || v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		fatalf("%s: invalid float %q: %v", k, v, err)
	}
	return f
}

// fatalf reports a fatal misconfiguration. Config errors are unrecoverable and
// must stop startup loudly rather than boot with a wrong value.
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "config: "+format+"\n", args...)
	os.Exit(2)
}
