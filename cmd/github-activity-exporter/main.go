// Command github-activity-exporter collects GitHub/Renovate metrics and exports them
// via OTLP (primary) with an optional Prometheus /metrics bridge (secondary).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/exporter"
	ghclient "github.com/gjed/github-activity-exporter/internal/github"
	"github.com/gjed/github-activity-exporter/internal/health"
)

// Set at build time via -ldflags (see Dockerfile).
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("github-activity-exporter %s (commit %s, built %s)\n", version, commit, date)
		return
	}

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

	// ── Start health server ───────────────────────────────────────────────────
	// Started before the credential checks so /healthz answers during startup.
	tracker := health.NewTracker(cfg.Health.StallTimeout, cfg.Health.UnauthorizedCycles)
	for _, t := range cfg.Targets {
		tracker.Register(t.Name)
	}
	healthSrv, err := startHealthServer(cfg.Health.ListenAddress, tracker.Handler(), logger)
	if err != nil {
		logger.Error("failed to start health server", "addr", cfg.Health.ListenAddress, "err", err)
		os.Exit(1)
	}
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := healthSrv.Shutdown(shutCtx); err != nil {
			logger.Error("health server shutdown error", "err", err)
		}
	}()

	// ── Build OTLP provider ───────────────────────────────────────────────────
	exporterCfg := exporter.Config{
		OTLPEndpoint:       cfg.Export.OTLP.Endpoint,
		Protocol:           exporter.Protocol(cfg.Export.OTLP.Protocol),
		Headers:            cfg.Export.OTLP.Headers,
		TLSInsecure:        cfg.Export.OTLP.TLS.Insecure,
		CollectionInterval: cfg.CollectionInterval,
		PrometheusEnabled:  cfg.Export.Prometheus.Enabled,
		PrometheusAddr:     cfg.Export.Prometheus.ListenAddress,
	}

	provider, err := exporter.New(ctx, exporterCfg, logger)
	if err != nil {
		logger.Error("failed to initialise metrics provider", "err", err)
		os.Exit(1)
	}

	// SelfMetrics are registered on the global MeterProvider; each Runner
	// records scrape duration and API errors into them every collection cycle.
	selfMetrics, err := exporter.NewSelfMetrics()
	if err != nil {
		logger.Error("failed to register self-metrics", "err", err)
		os.Exit(1)
	}

	// ── Build per-target runners ──────────────────────────────────────────────
	runners, err := buildRunners(ctx, cfg, logger, selfMetrics, tracker,
		collector.WithInterval(exporterCfg.CollectionInterval),
		collector.WithSelfMetrics(selfMetrics),
		collector.WithFlush(provider.ForceFlush),
		collector.WithCycleObserver(tracker),
	)
	if err != nil {
		logger.Error("failed to build runners", "err", err)
		os.Exit(1)
	}

	logger.Info("github-activity-exporter started",
		"version", version,
		"commit", commit,
		"targets", len(cfg.Targets),
		"otlp_endpoint", exporterCfg.OTLPEndpoint,
		"protocol", exporterCfg.Protocol,
		"collection_interval", exporterCfg.CollectionInterval,
		"prometheus_enabled", exporterCfg.PrometheusEnabled,
		"health_listen_address", cfg.Health.ListenAddress,
	)

	// ── Start per-target goroutines ───────────────────────────────────────────
	// Each Runner owns its own collection interval. main.go just waits for
	// cancellation and coordinates shutdown.
	var wg sync.WaitGroup
	for i, r := range runners {
		name := cfg.Targets[i].Name
		tracker.LoopStarted(name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Run(ctx)
			if ctx.Err() == nil {
				logger.Error("collection loop exited before shutdown", "target", name)
				tracker.LoopExited(name)
			}
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
func buildRunners(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	selfMetrics *exporter.SelfMetrics,
	tracker *health.Tracker,
	opts ...collector.RunnerOption,
) ([]*collector.Runner, error) {
	runners := make([]*collector.Runner, 0, len(cfg.Targets))
	for _, t := range cfg.Targets {
		r, err := buildTargetRunner(ctx, t, logger, selfMetrics, tracker, opts...)
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
	selfMetrics *exporter.SelfMetrics,
	tracker *health.Tracker,
	opts ...collector.RunnerOption,
) (*collector.Runner, error) {
	auth, err := buildAuthenticator(t.Auth, t.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("build authenticator: %w", err)
	}

	ghc, err := ghclient.NewClient(auth,
		ghclient.WithLogger(logger),
		ghclient.WithBaseURL(t.BaseURL),
		// The hook runs in the client transport and has no context.
		ghclient.WithRateLimitHook(func(s ghclient.RateLimitState) {
			selfMetrics.RecordRateLimit(context.Background(), t.Name, s.Resource, s.Remaining, s.ResetAt)
		}),
		ghclient.WithAuthResultHook(func(unauthorized bool) {
			tracker.AuthResult(t.Name, unauthorized)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("build github client: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := auth.Ping(pingCtx); err != nil {
		return nil, fmt.Errorf("credential ping failed: %w", err)
	}
	tracker.CredentialChecked(t.Name)

	disc := discovery.New(t, ghc.REST().Repositories, discovery.WithLogger(logger))

	otelMeter := otel.GetMeterProvider().Meter("github.com/gjed/github-activity-exporter/" + t.Name)

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
	}, otelMeter, logger, opts...)
}

// buildAuthenticator creates an Authenticator from the target auth config,
// talking to the target's REST API root.
func buildAuthenticator(a config.Auth, baseURL string) (ghclient.Authenticator, error) {
	if a.Token != "" {
		return ghclient.NewPATAuthenticatorWithBase(a.Token, baseURL), nil
	}
	opts := ghclient.AppAuthOptions{
		AppID:            a.App.AppID,
		InstallationID:   a.App.InstallationID,
		PrivateKeyPath:   a.App.PrivateKeyPath,
		PrivateKeyBase64: a.App.PrivateKeyValue,
		BaseURL:          baseURL,
	}
	return ghclient.NewAppAuthenticator(opts)
}

// startHealthServer binds addr synchronously, so a bad address fails startup,
// then serves h in the background.
func startHealthServer(addr string, h http.Handler, logger *slog.Logger) (*http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{
		Handler:      h,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	go func() {
		logger.Info("health server listening", "addr", ln.Addr().String())
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("health server error", "err", err)
		}
	}()
	return srv, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
