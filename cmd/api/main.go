// SitePulse API entrypoint.
//
// This file is intentionally the only place that knows how everything is
// wired together — config -> logger -> DB pool -> repositories -> services
// -> handlers -> router -> HTTP server. This is manual dependency
// injection: no framework/container, just passing concrete things into
// constructors that accept interfaces. It's more typing than a DI
// framework, but every dependency is visible by reading top-to-bottom,
// which matters a lot when someone (including future you) is debugging at
// 2am.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/yourname/sitepulse/internal/checker"
	"github.com/yourname/sitepulse/internal/config"
	"github.com/yourname/sitepulse/internal/handler"
	"github.com/yourname/sitepulse/internal/logger"
	"github.com/yourname/sitepulse/internal/notify"
	"github.com/yourname/sitepulse/internal/repository/postgres"
	"github.com/yourname/sitepulse/internal/scheduler"
	"github.com/yourname/sitepulse/internal/security"
	"github.com/yourname/sitepulse/internal/server"
	"github.com/yourname/sitepulse/internal/service"
)

func main() {
	// .env is only used for local development; in Docker/Railway, env vars
	// are injected directly and this file won't exist — Load() failing
	// silently here (ignoring the error) is intentional and correct.
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	log := logger.New(cfg.Env)
	log.Info("starting sitepulse", "env", cfg.Env, "port", cfg.HTTPPort)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dbPool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()
	log.Info("connected to database")

	userRepo := postgres.NewUserRepository(dbPool)
	refreshRepo := postgres.NewRefreshTokenRepository(dbPool)
	monitorRepo := postgres.NewMonitorRepository(dbPool)
	checkRepo := postgres.NewCheckRepository(dbPool)
	incidentRepo := postgres.NewIncidentRepository(dbPool)
	tokenIssuer := security.NewTokenIssuer(cfg.JWTSecret)

	authService := service.NewAuthService(userRepo, refreshRepo, tokenIssuer)
	monitorService := service.NewMonitorService(monitorRepo, checkRepo, incidentRepo)
	dashboardService := service.NewDashboardService(monitorRepo, checkRepo)
	userService := service.NewUserService(userRepo)

	// --- Notifications ---
	//
	// LoggingNotifier is always included — even with email/webhook
	// configured, seeing every alert in the server log is useful. Email
	// and webhook notifiers are only added if their config is actually
	// present; each is a safe no-op internally if empty, but skipping
	// construction entirely when unconfigured keeps the notifier chain
	// visibly honest about what's really active.
	notifiers := []notify.Notifier{notify.NewLoggingNotifier(log)}
	if cfg.SMTPHost != "" {
		notifiers = append(notifiers, notify.NewEmailNotifier(notify.EmailConfig{
			Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword, From: cfg.SMTPFrom, To: cfg.SMTPTo,
		}, log))
		log.Info("email notifications enabled", "smtp_host", cfg.SMTPHost)
	}
	if cfg.WebhookURL != "" {
		notifiers = append(notifiers, notify.NewWebhookNotifier(cfg.WebhookURL, log))
		log.Info("webhook notifications enabled")
	}
	notifier := notify.NewMultiNotifier(notifiers...)

	incidentService := service.NewIncidentService(incidentRepo, monitorRepo, notifier, log)
	certWatchService := service.NewCertWatchService(monitorRepo, notifier, cfg.CertExpiryWarnDays, log)

	// --- Concurrent monitoring engine ---
	//
	// Every completed check now fans out to four places: the log, Postgres
	// (durable history), the incident detector, and the cert-expiry
	// watcher. None of these know the others exist — MultiRecorder is the
	// only thing aware there's more than one. incidentService and
	// certWatchService are passed here as checker.ResultRecorder even
	// though the service package never imports checker — Go's structural
	// typing accepts them because their Record methods already match the
	// interface shape.
	recorder := checker.NewMultiRecorder(
		checker.NewLoggingRecorder(log),
		checker.NewPersistingRecorder(checkRepo, log),
		incidentService,
		certWatchService,
	)
	pool := checker.NewWorkerPool(cfg.CheckerPoolSize, cfg.SlowResponseThresh, log)
	sched := scheduler.New(cfg.SchedulerTick, monitorRepo, pool, recorder, log)

	// Runs as a background goroutine for the lifetime of the process,
	// sharing the same top-level ctx as the HTTP server below — so a
	// SIGINT/SIGTERM triggers both the HTTP server's graceful shutdown AND
	// the scheduler's drain-and-stop sequence at the same moment, not one
	// after the other.
	schedDone := make(chan struct{})
	go func() {
		defer close(schedDone)
		sched.Run(ctx)
	}()

	docsHandler, err := handler.NewDocsHandler()
	if err != nil {
		// An embed.FS read failure here means the binary itself was built
		// wrong (openapi.yaml missing at compile time) — this can only
		// happen from a broken build, never from runtime conditions, so
		// failing fast at startup is correct: better to catch a bad build
		// immediately than serve a broken /docs route silently forever.
		log.Error("failed to load embedded API docs", "error", err)
		os.Exit(1)
	}

	handlers := server.Handlers{
		Health:    handler.NewHealthHandler(),
		Auth:      handler.NewAuthHandler(authService, log, cfg.Env == "production"),
		Monitor:   handler.NewMonitorHandler(monitorService, log),
		Dashboard: handler.NewDashboardHandler(dashboardService),
		User:      handler.NewUserHandler(userService, log),
		Docs:      docsHandler,
	}
	router := server.NewRouter(log, handlers, tokenIssuer)

	srv := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// --- Graceful shutdown ---
	//
	// Why this matters: without it, a deploy or crash mid-request just
	// severs in-flight connections. A user's request silently dies, and
	// if we're mid-way through, say, writing a check result to Postgres,
	// we could leave data in a half-written state.
	//
	// The pattern: run ListenAndServe in a goroutine (it blocks), then
	// block main() on a context that's cancelled by SIGINT/SIGTERM
	// (signal.NotifyContext). Once that fires, call srv.Shutdown(ctx),
	// which stops accepting new connections but lets in-flight ones
	// finish, up to a bounded timeout.
	serverErrors := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case err := <-serverErrors:
		log.Error("server failed to start", "error", err)
		os.Exit(1)

	case <-ctx.Done():
		stop() // restore default signal behavior in case shutdown itself hangs
		log.Info("shutdown signal received, draining connections...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("graceful shutdown failed, forcing close", "error", err)
			_ = srv.Close()
		}

		// --- Wait for the scheduler to finish draining ---
		//
		// sched.Run is already unwinding concurrently (it shares ctx with
		// the HTTP server), but we don't want main() to return — and
		// defer dbPool.Close() to fire — while a worker might still be
		// mid-flight on a check or the result consumer is still writing
		// out the last few results. A bounded wait here (not indefinite)
		// means a genuinely stuck shutdown still lets the process exit
		// rather than hang forever.
		select {
		case <-schedDone:
			log.Info("scheduler drained cleanly")
		case <-time.After(10 * time.Second):
			log.Warn("scheduler did not drain within timeout, proceeding with shutdown anyway")
		}

		log.Info("shutdown complete")
	}
}
