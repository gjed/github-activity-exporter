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

	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/exporter"
	ghclient "github.com/gjed/github-activity-exporter/internal/github"
	"github.com/gjed/github-activity-exporter/internal/health"
	"github.com/gjed/github-activity-exporter/internal/semconv"
	"github.com/gjed/github-activity-exporter/internal/telemetry"
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
	// One resource for the product MeterProvider and every self-telemetry provider.
	res, err := telemetry.NewResource(ctx, version)
	if err != nil {
		logger.Error("failed to build resource", "err", err)
		os.Exit(1)
	}

	exporterCfg := exporter.Config{
		OTLPEndpoint:      cfg.Export.OTLP.Endpoint,
		Protocol:          exporter.Protocol(cfg.Export.OTLP.Protocol),
		Headers:           cfg.Export.OTLP.Headers,
		TLSInsecure:       cfg.Export.OTLP.TLS.Insecure,
		ExportInterval:    cfg.Export.Interval,
		PrometheusEnabled: cfg.Export.Prometheus.Enabled,
		PrometheusAddr:    cfg.Export.Prometheus.ListenAddress,
		Resource:          res,
		Version:           version,
	}

	provider, err := exporter.New(ctx, exporterCfg, logger)
	if err != nil {
		logger.Error("failed to initialise metrics provider", "err", err)
		os.Exit(1)
	}

	// ── Build self-telemetry providers ────────────────────────────────────────
	// Separate from the product MeterProvider: no telemetry setting or OTEL_*
	// toggle changes what the product metrics export.
	tel, err := telemetry.New(ctx, telemetry.Settings{
		Config:               cfg.Telemetry,
		OTLP:                 cfg.Export.OTLP,
		Resource:             res,
		Version:              version,
		PrometheusRegisterer: provider.PrometheusRegisterer(),
		Logger:               logger,
	})
	if err != nil {
		logger.Error("failed to initialise self-telemetry", "err", err)
		os.Exit(1)
	}

	// Every product instrument, self-metrics included, comes from one meter
	// with the exporter's instrumentation scope. Each Runner records scrape
	// duration and API errors into the self-metrics every collection cycle.
	meter := provider.Meter()
	selfMetrics, err := exporter.NewSelfMetrics(meter)
	if err != nil {
		logger.Error("failed to register self-metrics", "err", err)
		os.Exit(1)
	}

	// ── Build per-target runners ──────────────────────────────────────────────
	// GitHub HTTP client metrics go on the self-telemetry MeterProvider; with
	// self-telemetry metrics inactive the GitHub transport is not instrumented.
	var httpMeterProvider metric.MeterProvider
	if tel.Active(telemetry.SignalMetrics) {
		httpMeterProvider = tel.MeterProvider()
	}
	runners, err := buildRunners(ctx, cfg, logger, meter, httpMeterProvider, selfMetrics, tracker,
		collector.WithInterval(cfg.CollectionInterval),
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
		"collection_interval", cfg.CollectionInterval,
		"export_interval", cfg.Export.Interval,
		"prometheus_enabled", exporterCfg.PrometheusEnabled,
		"health_listen_address", cfg.Health.ListenAddress,
		"self_telemetry", activeSignals(tel),
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
	if err := tel.Shutdown(shutCtx); err != nil {
		logger.Error("self-telemetry shutdown error", "err", err)
	}
}

// activeSignals lists the active self-telemetry signals for the startup log.
func activeSignals(tel *telemetry.Telemetry) []string {
	active := []string{}
	for _, s := range telemetry.Signals {
		if tel.Active(s) {
			active = append(active, string(s))
		}
	}
	return active
}

// buildRunners creates one Runner per target with its own GitHub client and collectors.
func buildRunners(
	ctx context.Context,
	cfg *config.Config,
	logger *slog.Logger,
	meter metric.Meter,
	httpMeterProvider metric.MeterProvider,
	selfMetrics *exporter.SelfMetrics,
	tracker *health.Tracker,
	opts ...collector.RunnerOption,
) ([]*collector.Runner, error) {
	runners := make([]*collector.Runner, 0, len(cfg.Targets))
	for _, t := range cfg.Targets {
		r, err := buildTargetRunner(ctx, t, logger, meter, httpMeterProvider, selfMetrics, tracker, opts...)
		if err != nil {
			return nil, fmt.Errorf("target %q: %w", t.Name, err)
		}
		runners = append(runners, r)
	}
	return runners, nil
}

// buildTargetRunner builds a Runner for a single config.Target. A nil
// httpMeterProvider leaves the GitHub HTTP transport uninstrumented.
func buildTargetRunner(
	ctx context.Context,
	t config.Target,
	logger *slog.Logger,
	otelMeter metric.Meter,
	httpMeterProvider metric.MeterProvider,
	selfMetrics *exporter.SelfMetrics,
	tracker *health.Tracker,
	opts ...collector.RunnerOption,
) (*collector.Runner, error) {
	transport, err := ghclient.NewTransport(t.BaseURL, httpMeterProvider)
	if err != nil {
		return nil, fmt.Errorf("build github transport: %w", err)
	}

	auth, err := buildAuthenticator(t.Auth, t.BaseURL, &http.Client{Transport: transport})
	if err != nil {
		return nil, fmt.Errorf("build authenticator: %w", err)
	}

	ghc, err := ghclient.NewClient(auth,
		ghclient.WithLogger(logger),
		ghclient.WithBaseURL(t.BaseURL),
		ghclient.WithBaseTransport(transport),
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

	disc := discovery.New(t, ghc.REST(),
		discovery.WithLogger(logger),
		discovery.WithRefreshFailed(func() {
			selfMetrics.RecordAPIError(context.Background(), t.Name, semconv.AttrGitHubApiEndpointRestRepos)
		}),
	)

	prCfg := collector.PRCollectorConfig{LookbackDays: 30}
	prColl, err := collector.NewPRCollector(ghc.GraphQL(), t.Filters.PRs, prCfg, otelMeter, logger)
	if err != nil {
		return nil, fmt.Errorf("build PR collector: %w", err)
	}

	issColl, err := collector.NewIssueCollector(
		ghc,
		t.Filters.Issues,
		otelMeter,
		logger,
		collector.WithGraphQLBatchSize(t.BatchSize()),
	)
	if err != nil {
		return nil, fmt.Errorf("build issue collector: %w", err)
	}

	dashColl, err := newDashboardCollector(t, ghc, otelMeter, logger)
	if err != nil {
		return nil, fmt.Errorf("build dashboard collector: %w", err)
	}

	r, err := collector.NewRunner(t, disc, collector.RunnerCollectors{
		PR:        prColl,
		Issue:     issColl,
		Dashboard: dashColl,
	}, otelMeter, logger, opts...)
	if err != nil {
		return nil, err
	}

	// main runs the Runner with this ctx, so the repo list is refreshed for
	// as long as the collection loop runs. An explicit repo list bypasses
	// org discovery and never changes, so it is not refreshed.
	if len(t.Repos) == 0 {
		go disc.Start(ctx)
	}
	return r, nil
}

// newDashboardCollector builds the Renovate Dependency Dashboard collector,
// or returns nil when the target does not enable it; the Runner skips a nil
// collector.
func newDashboardCollector(
	t config.Target,
	gql collector.BatchGraphQLClient,
	meter metric.Meter,
	logger *slog.Logger,
) (*collector.DashboardCollector, error) {
	if !t.RenovateDashboard.Enabled {
		return nil, nil
	}
	return collector.NewDashboardCollector(
		gql,
		envOr("RENOVATE_BOT_LOGIN", "renovate[bot]"),
		meter,
		logger,
		collector.WithGraphQLBatchSize(t.BatchSize()),
	)
}

// buildAuthenticator creates an Authenticator from the target auth config,
// talking to the target's REST API root through client.
func buildAuthenticator(a config.Auth, baseURL string, client *http.Client) (ghclient.Authenticator, error) {
	if a.Token != "" {
		return ghclient.NewPATAuthenticatorWithBase(a.Token, baseURL, client), nil
	}
	opts := ghclient.AppAuthOptions{
		AppID:            a.App.AppID,
		InstallationID:   a.App.InstallationID,
		PrivateKeyPath:   a.App.PrivateKeyPath,
		PrivateKeyBase64: a.App.PrivateKeyValue,
		BaseURL:          baseURL,
		HTTPClient:       client,
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
