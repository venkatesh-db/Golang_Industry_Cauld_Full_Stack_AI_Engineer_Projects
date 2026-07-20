// Package config loads and validates process configuration. Secrets required
// for correctness (the webhook signing secret) are validated present at startup
// so the process fails fast rather than accepting unverifiable webhooks.
package config

import (
	"fmt"
	"os"
	"time"
)

// Config is the validated process configuration.
type Config struct {
	HTTPAddr          string        // listen address, e.g. ":8080"
	WebhookSecret     string        // provider signing secret; must be present
	ReconcileInterval time.Duration // how often the pull-leg cron runs
}

// Load reads config from the environment and validates it. It returns an error
// (never a partially-valid Config) when a required value is missing.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:          getenv("HTTP_ADDR", ":8080"),
		WebhookSecret:     os.Getenv("WEBHOOK_SECRET"),
		ReconcileInterval: getdur("RECONCILE_INTERVAL", time.Hour),
	}
	if c.WebhookSecret == "" {
		return Config{}, fmt.Errorf("config: WEBHOOK_SECRET is required (refusing to accept unverifiable webhooks)")
	}
	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getdur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
