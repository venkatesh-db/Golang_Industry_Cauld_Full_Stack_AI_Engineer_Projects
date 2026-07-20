// Package social contains the Instagram-style engagement notification feature.
package social

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config intentionally keeps runtime tuning close to the feature. The Kafka
// topics are code-owned contracts; environment variables configure topology,
// not arbitrary topic names.
type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	KafkaBrokers    []string
	ConsumerPrefix  string
	OutboxInterval  time.Duration
	OutboxBatchSize int
}

func LoadConfig() (Config, error) {
	c := Config{
		HTTPAddr:        env("HTTP_ADDR", ":8080"),
		DatabaseURL:     env("DATABASE_URL", "postgres://socialpulse:socialpulse@localhost:5433/socialpulse?sslmode=disable"),
		KafkaBrokers:    splitCSV(env("KAFKA_BROKERS", "localhost:9092")),
		ConsumerPrefix:  env("KAFKA_CONSUMER_PREFIX", "socialpulse"),
		OutboxInterval:  envDuration("OUTBOX_INTERVAL", 250*time.Millisecond),
		OutboxBatchSize: envInt("OUTBOX_BATCH_SIZE", 50),
	}
	if len(c.KafkaBrokers) == 0 {
		return Config{}, fmt.Errorf("KAFKA_BROKERS must contain at least one broker")
	}
	if c.OutboxInterval <= 0 || c.OutboxBatchSize < 1 || c.OutboxBatchSize > 500 {
		return Config{}, fmt.Errorf("OUTBOX_INTERVAL must be positive and OUTBOX_BATCH_SIZE must be in [1,500]")
	}
	return c, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if item := strings.TrimSpace(part); item != "" {
			out = append(out, item)
		}
	}
	return out
}
