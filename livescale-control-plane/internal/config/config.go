// Package config centralizes every tunable for the control plane. No magic
// numbers live in the hot path — they are all here with documented defaults.
package config

import (
	"errors"
	"os"
	"strconv"
	"time"
)

// devHMACKey is the well-known default used for local development. Shipping it
// to a non-dev environment would make every token forgeable (M2).
const devHMACKey = "livescale-dev-secret-change-me"

// Env reports the deployment environment (empty/"dev" is local).
func (c Config) Env() string { return os.Getenv("LS_ENV") }

// Validate enforces safety invariants at startup. It fails fast when the
// insecure dev HMAC key would be used outside a dev environment.
func (c Config) Validate() error {
	if string(c.HMACKey) == devHMACKey && c.Env() != "" && c.Env() != "dev" {
		return errors.New("config: refusing to start in LS_ENV=" + c.Env() +
			" with the default dev HMAC key — set LS_HMAC_KEY")
	}
	return nil
}

// Config holds all runtime tunables. Values are read once at startup and then
// treated as immutable.
type Config struct {
	Addr string // listen address, e.g. ":8080"

	// Concurrency sharding. ShardCount MUST be a power of two so the shard
	// index can be computed with a mask instead of a modulo on the hot path.
	ShardCount int

	// Admission control (ADR-002). Inflight requests above SoftMax shed
	// low-priority routes; above HardMax shed everything. Protects playback.
	SoftMaxInflight int
	HardMaxInflight int

	// Session lifecycle (ADR-001). A session lives SessionTTL past its last
	// heartbeat; the sweeper reclaims expired sessions every SweepInterval.
	SessionTTL    time.Duration
	SweepInterval time.Duration

	// HMACKey verifies playback tokens. Verify-only — we never mint identities.
	HMACKey []byte

	ShutdownGrace time.Duration
}

// Default returns production-shaped defaults sized for the single-box proof.
func Default() Config {
	return Config{
		Addr:            ":8080",
		ShardCount:      256,
		SoftMaxInflight: 8000,
		HardMaxInflight: 20000,
		SessionTTL:      30 * time.Second,
		SweepInterval:   5 * time.Second,
		HMACKey:         []byte("livescale-dev-secret-change-me"),
		ShutdownGrace:   10 * time.Second,
	}
}

// FromEnv overlays environment variables onto the defaults. Unset vars keep
// their default; malformed values fall back to the default (fail-soft config).
func FromEnv() Config {
	c := Default()
	if v := os.Getenv("LS_ADDR"); v != "" {
		c.Addr = v
	}
	if v := os.Getenv("LS_HMAC_KEY"); v != "" {
		c.HMACKey = []byte(v)
	}
	c.ShardCount = envInt("LS_SHARDS", c.ShardCount)
	c.SoftMaxInflight = envInt("LS_SOFT_MAX", c.SoftMaxInflight)
	c.HardMaxInflight = envInt("LS_HARD_MAX", c.HardMaxInflight)
	c.SessionTTL = envDur("LS_SESSION_TTL", c.SessionTTL)
	c.SweepInterval = envDur("LS_SWEEP", c.SweepInterval)
	return c.normalized()
}

// normalized enforces invariants the rest of the code relies on.
func (c Config) normalized() Config {
	if c.ShardCount < 1 || c.ShardCount&(c.ShardCount-1) != 0 {
		c.ShardCount = 256 // must be power of two
	}
	if c.HardMaxInflight < c.SoftMaxInflight {
		c.HardMaxInflight = c.SoftMaxInflight * 2
	}
	return c
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
