package config

import (
	"os"
	"strings"
)

type Config struct {
	DatabaseURL  string
	KafkaBrokers []string
	HTTPAddr     string
	DriverAddr   string
}

func FromEnv() Config {
	return Config{
		DatabaseURL:  value("DATABASE_URL", "postgres://rapido:rapido@localhost:5432/rapido?sslmode=disable"),
		KafkaBrokers: strings.Split(value("KAFKA_BROKERS", "localhost:9092"), ","),
		HTTPAddr:     value("HTTP_ADDR", ":8080"),
		DriverAddr:   value("DRIVER_HTTP_ADDR", ":8081"),
	}
}

func value(key, fallback string) string {
	if current := os.Getenv(key); current != "" {
		return current
	}
	return fallback
}
