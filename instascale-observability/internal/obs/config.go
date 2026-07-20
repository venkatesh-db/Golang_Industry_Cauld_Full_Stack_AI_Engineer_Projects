package obs

import (
	"os"
	"strconv"
	"time"
)

// Env returns the env var or a default.
func Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// EnvInt returns the env var parsed as int, or a default.
func EnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// EnvBool returns true for "1"/"true"/"yes" (case-insensitive), else def.
func EnvBool(key string, def bool) bool {
	v := os.Getenv(key)
	switch v {
	case "1", "true", "TRUE", "True", "yes":
		return true
	case "0", "false", "FALSE", "False", "no":
		return false
	default:
		return def
	}
}

// EnvMS returns an env var (in milliseconds) as a Duration.
func EnvMS(key string, defMS int) time.Duration {
	return time.Duration(EnvInt(key, defMS)) * time.Millisecond
}
