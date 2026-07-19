// Package config centralizes all environment-driven settings in one place.
//
// Why not just call os.Getenv() scattered through the codebase? Two reasons:
//  1. Discoverability — one file tells you every knob the app has.
//  2. Fail-fast — we validate/parse once at startup. A malformed
//     CHECK_WORKER_POOL_SIZE should crash the app in the first 10ms with a
//     clear error, not silently misbehave three hours into a shift.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Env         string // "development" | "production"
	HTTPPort    string
	DatabaseURL string
	JWTSecret   string

	// Scheduler / worker pool tuning — these exist because "how many
	// checks run concurrently" is a capacity decision that should be
	// configurable per-environment, not hardcoded.
	CheckerPoolSize    int           // number of worker goroutines running HTTP checks
	CheckerTimeout     time.Duration // per-check HTTP timeout
	SchedulerTick      time.Duration // how often the scheduler wakes up to look for due monitors
	SlowResponseThresh time.Duration // response time above which a successful check is flagged "slow"

	// --- Notifications (Phase 7) — all optional, empty = disabled ---
	// A monitoring platform that requires SMTP credentials just to start
	// up would be a poor experience for local development and for anyone
	// trying this project fresh, so every field here defaults to empty,
	// and each Notifier implementation treats an empty config as "do
	// nothing" rather than an error.
	SMTPHost     string
	SMTPPort     string
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPTo       string
	WebhookURL   string

	// CertExpiryWarnThreshold: warn once a monitor's TLS cert is within
	// this many days of expiring.
	CertExpiryWarnDays int
}

func Load() (*Config, error) {
	cfg := &Config{
		Env:         getEnv("APP_ENV", "development"),
		HTTPPort:    getEnv("HTTP_PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", ""),
		JWTSecret:   getEnv("JWT_SECRET", ""),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("config: DATABASE_URL is required")
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("config: JWT_SECRET is required")
	}

	poolSize, err := getEnvInt("CHECKER_POOL_SIZE", 10)
	if err != nil {
		return nil, err
	}
	cfg.CheckerPoolSize = poolSize

	checkerTimeout, err := getEnvDuration("CHECKER_TIMEOUT", 10*time.Second)
	if err != nil {
		return nil, err
	}
	cfg.CheckerTimeout = checkerTimeout

	schedulerTick, err := getEnvDuration("SCHEDULER_TICK", 5*time.Second)
	if err != nil {
		return nil, err
	}
	cfg.SchedulerTick = schedulerTick

	slowThresh, err := getEnvDuration("SLOW_RESPONSE_THRESHOLD", 2*time.Second)
	if err != nil {
		return nil, err
	}
	cfg.SlowResponseThresh = slowThresh

	cfg.SMTPHost = getEnv("SMTP_HOST", "")
	cfg.SMTPPort = getEnv("SMTP_PORT", "587")
	cfg.SMTPUsername = getEnv("SMTP_USERNAME", "")
	cfg.SMTPPassword = getEnv("SMTP_PASSWORD", "")
	cfg.SMTPFrom = getEnv("SMTP_FROM", "")
	cfg.SMTPTo = getEnv("SMTP_TO", "")
	cfg.WebhookURL = getEnv("WEBHOOK_URL", "")

	certExpiryDays, err := getEnvInt("CERT_EXPIRY_WARN_DAYS", 14)
	if err != nil {
		return nil, err
	}
	cfg.CertExpiryWarnDays = certExpiryDays

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return n, nil
}

func getEnvDuration(key string, fallback time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a duration (e.g. '10s'): %w", key, err)
	}
	return d, nil
}
