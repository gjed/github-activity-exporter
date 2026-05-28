// Command renovate-exporter collects GitHub/Renovate metrics and exports them
// via OTLP (primary) with an optional Prometheus /metrics bridge (secondary).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/gjed/renovate-exporter/internal/collector"
	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/discovery"
	"github.com/gjed/renovate-exporter/internal/exporter"
	ghclient "github.com/gjed/renovate-exporter/internal/github"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// ── Load config ───────────────────────────────────────────────────────────
	cfgPath := envOr("CONFIG_FILE", "config.yaml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Error("failed to load config", "path", cfgPath, "err", err)
		os.Exit(1)
	}

	// ── Build OTLP provider ───────────────────────────────────────────────────
	exporterCfg := exporter.Config{
		OTLPEndpoint:       envOr("OTLP_ENDPOINT", "http://localhost:4318"),
		Protocol:           exporter.Protocol(envOr("OTLP_PROTOCOL", "http")),
		CollectionInterval: mustDuration(envOr("COLLECTION_INTERVAL", "5m")),
		PrometheusEnabled:  os.Getenv("PROMETHEUS_ENABLED") == "true",
		PrometheusAddr:     envOr("PROMETHEUS_ADDR", ":9090"),
		TLSInsecure:        os.Getenv("OTLP_TLS_INSECURE") == "true",
	}
	if authHeader := os.Getenv("OTLP_AUTH_HEADER"); authHeader != "" {
		exporterCfg.Headers = map[string]string{"Authorization": authHeader}
	}

	provider, err := exporter.New(ctx, exporterCfg, logger)
	if err != nil {
		logger.Error("failed to initialise metrics provider", "err", err)
		os.Exit(1)
	}

	// SelfMetrics are wired to the global MeterProvider. The PeriodicReader
	// will flush them on each collection interval alongside business metrics.
	if _, err = exporter.NewSelfMetrics(); err != nil {
		logger.Error("failed to register self-metrics", "err", err)
		os.Exit(1)
	}

	// ── Build per-target runners ──────────────────────────────────────────────
	runners, err := buildRunners(ctx, cfg, logger)
	if err != nil {
		logger.Error("failed to build runners", "err", err)
		os.Exit(1)
	}

	logger.Info("renovate-exporter started",
		"targets", len(cfg.Targets),
		"otlp_endpoint", exporterCfg.OTLPEndpoint,
		"protocol", exporterCfg.Protocol,
		"collection_interval", exporterCfg.CollectionInterval,
		"prometheus_enabled", exporterCfg.PrometheusEnabled,
	)

	// ── Start per-target goroutines ───────────────────────────────────────────
	// Each Runner owns its own collection interval. main.go just waits for
	// cancellation and coordinates shutdown.
	var wg sync.WaitGroup
	for _, r := range runners {
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Run(ctx)
		}()
	}

	// Block until SIGTERM/SIGINT.
	<-ctx.Done()

	logger.Info("shutting down — waiting for in-flight collections")
	wg.Wait()

	logger.Info("flushing metrics")
	shutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := provider.Shutdown(shutCtx); err != nil {
		logger.Error("shutdown error", "err", err)
	}
}

// buildRunners creates one Runner per target with its own GitHub client and collectors.
func buildRunners(ctx context.Context, cfg *config.Config, logger *slog.Logger) ([]*collector.Runner, error) {
	runners := make([]*collector.Runner, 0, len(cfg.Targets))
	for _, t := range cfg.Targets {
		r, err := buildTargetRunner(ctx, t, logger)
		if err != nil {
			return nil, fmt.Errorf("target %q: %w", t.Name, err)
		}
		runners = append(runners, r)
	}
	return runners, nil
}

// buildTargetRunner builds a Runner for a single config.Target.
func buildTargetRunner(
	ctx context.Context,
	t config.Target,
	logger *slog.Logger,
) (*collector.Runner, error) {
	auth, err := buildAuthenticator(t.Auth)
	if err != nil {
		return nil, fmt.Errorf("build authenticator: %w", err)
	}

	ghc, err := ghclient.NewClient(auth, ghclient.WithLogger(logger))
	if err != nil {
		return nil, fmt.Errorf("build github client: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := auth.Ping(pingCtx); err != nil {
		return nil, fmt.Errorf("credential ping failed: %w", err)
	}

	disc := discovery.New(t, ghc.REST().Repositories, discovery.WithLogger(logger))

	otelMeter := otel.GetMeterProvider().Meter("github.com/gjed/renovate-exporter/" + t.Name)

	prCfg := collector.PRCollectorConfig{
		MaxPRsPerRepo: 500,
		LookbackDays:  30,
	}
	prColl, err := collector.NewPRCollector(ghc.GraphQL(), t.Filters.PRs, prCfg, otelMeter, logger)
	if err != nil {
		return nil, fmt.Errorf("build PR collector: %w", err)
	}

	issColl, err := collector.NewIssueCollector(
		ghc.REST().Issues,
		t.Filters.Issues,
		otelMeter,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("build issue collector: %w", err)
	}

	botLogin := envOr("RENOVATE_BOT_LOGIN", "renovate[bot]")
	dashColl, err := collector.NewDashboardCollector(
		ghc.REST().Issues,
		botLogin,
		otelMeter,
		logger,
	)
	if err != nil {
		return nil, fmt.Errorf("build dashboard collector: %w", err)
	}

	return collector.NewRunner(t, disc, collector.RunnerCollectors{
		PR:        prColl,
		Issue:     issColl,
		Dashboard: dashColl,
	}, otelMeter, logger)
}

// buildAuthenticator creates an Authenticator from the target auth config.
func buildAuthenticator(a config.Auth) (ghclient.Authenticator, error) {
	if a.Token != "" {
		return ghclient.NewPATAuthenticator(a.Token), nil
	}
	opts := ghclient.AppAuthOptions{
		AppID:            a.App.AppID,
		InstallationID:   a.App.InstallationID,
		PrivateKeyPath:   a.App.PrivateKeyPath,
		PrivateKeyBase64: a.App.PrivateKeyValue,
	}
	return ghclient.NewAppAuthenticator(opts)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		slog.Error("invalid duration", "value", s, "err", err)
		os.Exit(1)
	}
	return d
}
