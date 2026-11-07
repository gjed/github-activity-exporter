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
	"strings"
	"time"

	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/gjed/github-activity-exporter/internal/telemetry"
)

// Provider bundles the MeterProvider with its shutdown function and optional
// Prometheus HTTP server.
type Provider struct {
	mp         *metric.MeterProvider
	promReg    *prometheus.Registry
	promServer *http.Server
	logger     *slog.Logger
	version    string
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

	res := cfg.Resource
	if res == nil {
		var err error
		if res, err = telemetry.NewResource(ctx, cfg.Version); err != nil {
			return nil, fmt.Errorf("exporter: build resource: %w", err)
		}
	}

	p := &Provider{logger: logger, version: cfg.Version}

	// ── Primary: OTLP exporter + PeriodicReader ──────────────────────────────
	otlpExp, err := p.newOTLPExporter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("exporter: build OTLP exporter: %w", err)
	}

	periodicReader := metric.NewPeriodicReader(
		otlpExp,
		metric.WithInterval(cfg.CollectionInterval),
		// Pinned: OTEL_METRIC_EXPORT_TIMEOUT must not reach the product pipeline.
		metric.WithTimeout(exportTimeout),
	)

	opts := []metric.Option{
		metric.WithResource(res),
		metric.WithReader(periodicReader),
	}

	// ── Secondary (opt-in): Prometheus bridge + ManualReader ─────────────────
	var promReg *prometheus.Registry
	if cfg.PrometheusEnabled {
		promReg = prometheus.NewRegistry()
		// NoTranslation exposes the registry metric names verbatim (e.g.
		// github.pr.count): no _total or unit suffix, no underscore escaping.
		// Histograms add only _bucket/_sum/_count. This is the naming contract
		// declared as annotations.prometheus in registry/signals.yaml; scrapers
		// need UTF-8 metric names.
		promExp, err := promexporter.New(
			promexporter.WithRegisterer(promReg),
			promexporter.WithTranslationStrategy(otlptranslator.NoTranslation),
		)
		if err != nil {
			return nil, fmt.Errorf("exporter: build Prometheus bridge: %w", err)
		}
		opts = append(opts, metric.WithReader(promExp))
	}

	p.mp = metric.NewMeterProvider(opts...)
	p.promReg = promReg
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

// Meter returns the product meter: scope telemetry.ScopeName, versioned with
// the build version. Every product instrument is created from it.
func (p *Provider) Meter() otelmetric.Meter {
	return p.mp.Meter(telemetry.ScopeName, otelmetric.WithInstrumentationVersion(p.version))
}

// PrometheusRegisterer returns the registry behind /metrics, or nil when the
// bridge is disabled. Self-telemetry registers its own reader on it.
func (p *Provider) PrometheusRegisterer() prometheus.Registerer {
	if p.promReg == nil {
		return nil
	}
	return p.promReg
}

// ForceFlush triggers an immediate export of all pending metrics. Each Runner
// calls it at the end of every collection cycle.
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

// The product OTLP exporter sets every option the SDK would otherwise read
// from OTEL_EXPORTER_OTLP_{,METRICS_}*, because the SDK applies the env first
// and options on top. Those variables configure self-telemetry; they must not
// change where or how the product metrics are pushed.
const (
	exportTimeout = 30 * time.Second // PeriodicReader export, SDK default
	pushTimeout   = 10 * time.Second // one OTLP request, SDK default
)

func (p *Provider) newHTTPExporter(ctx context.Context, cfg Config) (metric.Exporter, error) {
	// Set even for http:// (as nil), so OTEL_EXPORTER_OTLP_*CERTIFICATE cannot
	// add a TLS config the SDK rejects for plain HTTP.
	var tlsCfg *tls.Config
	switch {
	case cfg.TLSInsecure:
		tlsCfg = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in, documented
	case strings.HasPrefix(cfg.OTLPEndpoint, "https://"):
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	opts := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpointURL(cfg.OTLPEndpoint),
		otlpmetrichttp.WithHeaders(cfg.Headers), // nil also overrides the env
		otlpmetrichttp.WithTLSClientConfig(tlsCfg),
		otlpmetrichttp.WithCompression(otlpmetrichttp.NoCompression),
		otlpmetrichttp.WithTimeout(pushTimeout),
		otlpmetrichttp.WithTemporalitySelector(metric.DefaultTemporalitySelector),
		otlpmetrichttp.WithAggregationSelector(metric.DefaultAggregationSelector),
	}

	exp, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &errorCountingExporter{inner: exp, logger: p.logger, target: cfg.Target, version: cfg.Version}, nil
}

func (p *Provider) newGRPCExporter(ctx context.Context, cfg Config) (metric.Exporter, error) {
	// Credentials must be set via the otlpmetricgrpc options: the exporter
	// appends its own default TLS credentials after any WithDialOption, so
	// transport credentials passed as dial options are silently overridden.
	opts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.OTLPEndpoint),
	}
	if cfg.TLSInsecure {
		opts = append(opts, otlpmetricgrpc.WithInsecure())
	} else {
		opts = append(opts, otlpmetricgrpc.WithTLSCredentials(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
		})))
	}
	opts = append(opts,
		otlpmetricgrpc.WithHeaders(cfg.Headers), // nil also overrides the env
		// WithCompressor accepts only "gzip"; a later call option is the only
		// way to override the env compression with none.
		otlpmetricgrpc.WithDialOption(grpc.WithDefaultCallOptions(grpc.UseCompressor(encoding.Identity))),
		otlpmetricgrpc.WithTimeout(pushTimeout),
		otlpmetricgrpc.WithTemporalitySelector(metric.DefaultTemporalitySelector),
		otlpmetricgrpc.WithAggregationSelector(metric.DefaultAggregationSelector),
	)

	exp, err := otlpmetricgrpc.New(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &errorCountingExporter{inner: exp, logger: p.logger, target: cfg.Target, version: cfg.Version}, nil
}

func (p *Provider) startPrometheusServer(addr string, reg *prometheus.Registry) *http.Server {
	mux := http.NewServeMux()
	// OpenMetrics stays off: it requires counter names to end in _total, so
	// under NoTranslation it would type registry counters as unknown. Scrapers
	// that ask for OpenMetrics first (Prometheus 3.x and Alloy defaults) fall
	// back to Prometheus text 1.0.0, which keeps the counter type.
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

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
