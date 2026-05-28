// Package exporter wires the OTel MeterProvider with OTLP primary export
// and an optional Prometheus bridge secondary.
//
// Architecture:
//
//	MeterProvider
//	├── PeriodicReader (interval: CollectionInterval)
//	│   └── OTLP Exporter (HTTP or gRPC)  ← primary (required)
//	└── ManualReader
//	    └── Prometheus Bridge Exporter    ← secondary (opt-in)
//	            │
//	            └── /metrics HTTP handler
//
// Both readers read from the same in-memory metric state: one data model,
// two views. No re-querying the data source on Prometheus scrape.
package exporter

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Provider bundles the MeterProvider with its shutdown function and optional
// Prometheus HTTP server.
type Provider struct {
	mp         *metric.MeterProvider
	promServer *http.Server
	logger     *slog.Logger
}

// New builds and starts the MeterProvider. It returns a Provider whose
// Shutdown method must be called on application exit.
func New(ctx context.Context, cfg Config, logger *slog.Logger) (*Provider, error) {
	cfg.defaults()

	if cfg.OTLPEndpoint == "" {
		return nil, errors.New("exporter: OTLPEndpoint is required")
	}

	if logger == nil {
		logger = slog.Default()
	}

	res, err := buildResource(ctx)
	if err != nil {
		return nil, fmt.Errorf("exporter: build resource: %w", err)
	}

	p := &Provider{logger: logger}

	// ── Primary: OTLP exporter + PeriodicReader ──────────────────────────────
	otlpExp, err := p.newOTLPExporter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("exporter: build OTLP exporter: %w", err)
	}

	periodicReader := metric.NewPeriodicReader(
		otlpExp,
		metric.WithInterval(cfg.CollectionInterval),
	)

	opts := []metric.Option{
		metric.WithResource(res),
		metric.WithReader(periodicReader),
	}

	// ── Secondary (opt-in): Prometheus bridge + ManualReader ─────────────────
	var promReg *prometheus.Registry
	if cfg.PrometheusEnabled {
		promReg = prometheus.NewRegistry()
		// WithTranslationStrategy(NoUTF8EscapingWithSuffixes) preserves OTel
		// dotted metric names (e.g. github.pr.count) in the Prometheus exposition
		// output. This requires Grafana / Prometheus to be configured with the
		// UTF-8 name validation scheme (prometheus.model.NameValidationScheme=utf8).
		promExp, err := promexporter.New(
			promexporter.WithRegisterer(promReg),
			promexporter.WithTranslationStrategy(otlptranslator.NoUTF8EscapingWithSuffixes),
		)
		if err != nil {
			return nil, fmt.Errorf("exporter: build Prometheus bridge: %w", err)
		}
		opts = append(opts, metric.WithReader(promExp))
	}

	p.mp = metric.NewMeterProvider(opts...)
	otel.SetMeterProvider(p.mp)

	if cfg.PrometheusEnabled && promReg != nil {
		p.promServer = p.startPrometheusServer(cfg.PrometheusAddr, promReg)
	}

	return p, nil
}

// MeterProvider returns the underlying OTel MeterProvider.
// Callers use otel.GetMeterProvider() in production; this accessor is for tests.
func (p *Provider) MeterProvider() *metric.MeterProvider {
	return p.mp
}

// ForceFlush triggers an immediate export of all pending metrics.
// Useful in tests to ensure data reaches the OTLP receiver before assertions.
func (p *Provider) ForceFlush(ctx context.Context) error {
	return p.mp.ForceFlush(ctx)
}

// Shutdown flushes pending metrics and stops the Prometheus HTTP server.
// Call this on SIGTERM/SIGINT to ensure a final push is attempted.
func (p *Provider) Shutdown(ctx context.Context) error {
	var errs []error

	if p.promServer != nil {
		shutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := p.promServer.Shutdown(shutCtx); err != nil {
			errs = append(errs, fmt.Errorf("prometheus server shutdown: %w", err))
		}
	}

	if err := p.mp.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("meter provider shutdown: %w", err))
	}

	return errors.Join(errs...)
}

// ── internal helpers ─────────────────────────────────────────────────────────

// newOTLPExporter builds the OTLP exporter (HTTP or gRPC) and wraps it with
// error-counting middleware.
func (p *Provider) newOTLPExporter(ctx context.Context, cfg Config) (metric.Exporter, error) {
	switch cfg.Protocol {
	case ProtocolGRPC:
		return p.newGRPCExporter(ctx, cfg)
	default:
		return p.newHTTPExporter(ctx, cfg)
	}
}

func (p *Provider) newHTTPExporter(ctx context.Context, cfg Config) (metric.Exporter, error) {
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(cfg.OTLPEndpoint),
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlpmetrichttp.WithHeaders(cfg.Headers))
	}
	if cfg.TLSInsecure {
		opts = append(opts, otlpmetrichttp.WithTLSClientConfig(&tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // opt-in, documented
		}))
	}

	exp, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &errorCountingExporter{inner: exp, logger: p.logger, target: cfg.Target}, nil
}

func (p *Provider) newGRPCExporter(ctx context.Context, cfg Config) (metric.Exporter, error) {
	var dialOpts []grpc.DialOption
	if cfg.TLSInsecure {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
		})))
	}

	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
		otlpmetricgrpc.WithDialOption(dialOpts...),
	}
	if len(cfg.Headers) > 0 {
		opts = append(opts, otlpmetricgrpc.WithHeaders(cfg.Headers))
	}

	exp, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &errorCountingExporter{inner: exp, logger: p.logger, target: cfg.Target}, nil
}

func (p *Provider) startPrometheusServer(addr string, reg *prometheus.Registry) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		p.logger.Info("prometheus bridge listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			p.logger.Error("prometheus server error", "err", err)
		}
	}()

	return srv
}

func buildResource(ctx context.Context) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithOS(),
		resource.WithContainer(),
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceName("renovate-github-exporter"),
		),
	)
}
