// Package config loads server configuration from environment variables,
// with sane local-dev defaults so `go run ./cmd/server` works with zero
// setup beyond docker-compose up.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port             string
	BaseURL          string // public base URL used to build full short links, e.g. https://snap.sonofnos.com
	DatabaseURL      string
	RedisURL         string
	IDBlockSize      uint64
	LocalCacheSize   int
	LocalCacheTTL    time.Duration
	RateLimitPerMin  int           // create-link rate limit, per IP
	TrustedProxyHops int           // proxies appending to X-Forwarded-For (Render + Cloudflare = 2); 0 ignores the header
	AnalyticsFlush   time.Duration // how often buffered clicks are batch-written to Postgres
	AnalyticsBuffer  int
}

func Load() Config {
	// Render puts Cloudflare and its own router in front of every service, so
	// X-Forwarded-For arrives as "client, cloudflare, render". Elsewhere the
	// header is untrusted unless TRUSTED_PROXY_HOPS says otherwise.
	defaultHops := 0
	if os.Getenv("RENDER") != "" {
		defaultHops = 2
	}
	return Config{
		Port:             getEnv("PORT", "8080"),
		BaseURL:          getEnv("BASE_URL", "http://localhost:8080"),
		DatabaseURL:      getEnv("DATABASE_URL", "postgres://snaplink:snaplink@localhost:5433/snaplink?sslmode=disable"),
		RedisURL:         getEnv("REDIS_URL", "redis://localhost:6380/0"),
		IDBlockSize:      getEnvUint64("ID_BLOCK_SIZE", 1000),
		LocalCacheSize:   getEnvInt("LOCAL_CACHE_SIZE", 50_000),
		LocalCacheTTL:    getEnvDuration("LOCAL_CACHE_TTL", 30*time.Second),
		RateLimitPerMin:  getEnvInt("RATE_LIMIT_PER_MIN", 60),
		TrustedProxyHops: getEnvInt("TRUSTED_PROXY_HOPS", defaultHops),
		AnalyticsFlush:   getEnvDuration("ANALYTICS_FLUSH_INTERVAL", 250*time.Millisecond),
		AnalyticsBuffer:  getEnvInt("ANALYTICS_BUFFER_SIZE", 10_000),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvUint64(key string, fallback uint64) uint64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
