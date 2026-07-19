// Package logger configures structured logging via the standard library's
// log/slog (Go 1.21+).
//
// Why slog instead of a third-party lib like zerolog/zap? For a portfolio
// project, showing you reach for the stdlib solution first — and only
// bring in a dependency when the stdlib genuinely falls short — is itself
// a signal of judgment. slog gives us structured (key=value / JSON) logs,
// levels, and context-aware logging out of the box.
//
// "Structured" matters here specifically: SitePulse's core log lines
// ("check completed", "incident opened") have consistent fields
// (monitor_id, status, response_time_ms). Structured logs let you grep or
// pipe these into a log aggregator and query by field instead of regexing
// a sentence.
package logger

import (
	"log/slog"
	"os"
)

// New returns a slog.Logger. In production we emit JSON (machine-parseable,
// what you'd ship to Railway/Datadog/etc). In development we emit
// human-readable text, because nobody wants to eyeball JSON while iterating.
func New(env string) *slog.Logger {
	level := slog.LevelInfo
	if env == "development" {
		level = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	l := slog.New(handler)
	slog.SetDefault(l)
	return l
}
