package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all gateway configuration sourced from environment variables.
// Every field has a sensible default so the gateway runs with zero configuration.
type Config struct {
	// Server
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration

	// Upstream
	BackendURL string

	// Rate limiting
	// If RedisURL is set, the Redis-backed distributed limiter is used.
	// Otherwise the gateway falls back to the in-memory limiter.
	RedisURL     string
	RLCapacity   float64 // max tokens per bucket
	RLRefillRate float64 // tokens added per second
}

func Load() (*Config, error) {
	return &Config{
		Port:         getEnv("PORT", "8080"),
		ReadTimeout:  getDuration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout: getDuration("WRITE_TIMEOUT", 30*time.Second),
		IdleTimeout:  getDuration("IDLE_TIMEOUT", 60*time.Second),

		BackendURL: getEnv("BACKEND_URL", "https://httpbin.org"),

		RedisURL:     getEnv("REDIS_URL", ""),
		RLCapacity:   getFloat("RL_CAPACITY", 10),
		RLRefillRate: getFloat("RL_REFILL_RATE", 5),
	}, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func getDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
