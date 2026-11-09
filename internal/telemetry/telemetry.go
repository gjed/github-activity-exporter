package telemetry

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/log"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/credentials"

	"github.com/gjed/github-activity-exporter/internal/config"
)

// Signal is a self-telemetry signal.
type Signal string

// The self-telemetry signals.
const (
	SignalMetrics  Signal = "metrics"
	SignalTraces   Signal = "traces"
	SignalLogs     Signal = "logs"
	SignalProfiles Signal = "profiles"
)

// Signals lists every signal in a fixed order.
var Signals = []Signal{SignalMetrics, SignalTraces, SignalLogs, SignalProfiles}

// Settings is the input of New.
type Settings struct {
	// Config holds the telemetry toggles.
	Config config.Telemetry
	// OTLP is export.otlp, the destination of a signal with no OTLP endpoint
	// in the environment.
	OTLP config.OTLP
	// Resource is the resource shared with the product MeterProvider.
	Resource *resource.Resource
	// Version is the instrumentation scope version.
	Version string
	// PrometheusRegisterer, when set, also exposes self-telemetry metrics on
	// the Prometheus bridge's /metrics.
	PrometheusRegisterer prometheus.Registerer
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Getenv defaults to os.Getenv. It must agree with the process
	// environment, which the SDK exporters read for OTEL_EXPORTER_OTLP_*.
	Getenv func(string) string
}

// Telemetry holds the self-telemetry providers. Consumers take a provider
// from it: a real SDK provider when the signal is active, a no-op one when not.
type Telemetry struct {
	active  map[Signal]bool
	version string

	mp *sdkmetric.MeterProvider
	// producer is set when self-telemetry metrics inherit export.otlp: they
	// travel in the product push instead of their own.
	producer sdkmetric.Producer
	tp       *sdktrace.TracerProvider
	lp       *sdklog.LoggerProvider
	pr       *profiler
}

// New routes OTel SDK errors to Settings.Logger, resolves which signals are
// active and builds an SDK provider with an OTLP exporter for each active OTLP
// signal. Inactive signals get no provider and no exporter, so they open no
// connection. When traces or logs are active it sets the global
// TracerProvider or LoggerProvider; the global MeterProvider stays the
// product one.
func New(ctx context.Context, s Settings) (*Telemetry, error) {
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	logger := s.Logger
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Warn("opentelemetry error", "err", err)
	}))
	if s.Getenv == nil {
		s.Getenv = os.Getenv
	}
	if s.Resource == nil {
		s.Resource = resource.Default()
	}

	t := &Telemetry{active: resolveSignals(s.Config, s.Getenv, s.Logger), version: s.Version}

	if t.active[SignalMetrics] {
		mp, producer, err := newMeterProvider(ctx, s)
		if err != nil {
			return nil, fmt.Errorf("telemetry: metrics: %w", err)
		}
		t.mp, t.producer = mp, producer
		// Go runtime metrics (go.*) through callbacks on this provider;
		// go.schedule.duration comes from the producer on its readers.
		if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
			return nil, errors.Join(fmt.Errorf("telemetry: runtime metrics: %w", err), t.Shutdown(ctx))
		}
	}
	if t.active[SignalTraces] {
		exp, err := newTraceExporter(ctx, s)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("telemetry: traces: %w", err), t.Shutdown(ctx))
		}
		// No sampler option: the SDK applies OTEL_TRACES_SAMPLER and
		// OTEL_TRACES_SAMPLER_ARG, default parentbased_always_on.
		t.tp = sdktrace.NewTracerProvider(sdktrace.WithResource(s.Resource), sdktrace.WithBatcher(exp))
		otel.SetTracerProvider(t.tp)
	}
	if t.active[SignalLogs] {
		exp, err := newLogExporter(ctx, s)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("telemetry: logs: %w", err), t.Shutdown(ctx))
		}
		t.lp = sdklog.NewLoggerProvider(sdklog.WithResource(s.Resource), sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
		otel.SetLoggerProvider(t.lp)
	}
	if t.active[SignalProfiles] {
		// Profiles settings errors are logged, not returned: profiles stay
		// off and the exporter runs.
		if errs := profilesErrors(s.Config.Profiles); len(errs) > 0 {
			for _, key := range slices.Sorted(maps.Keys(errs)) {
				s.Logger.Error("invalid profiles setting, profiles are off", "key", key, "err", errs[key])
			}
			t.active[SignalProfiles] = false
		} else {
			t.pr = startProfiler(s.Config.Profiles, s.Logger)
		}
	}
	return t, nil
}

// Active reports whether a signal is active: telemetry.enabled, its own
// enabled, OTEL_SDK_DISABLED not true, OTEL_<SIGNAL>_EXPORTER not none and,
// for profiles, valid profiles settings.
func (t *Telemetry) Active(s Signal) bool { return t.active[s] }

// MeterProvider returns the self-telemetry MeterProvider, separate from the
// product one; a no-op provider when self-telemetry metrics are inactive.
func (t *Telemetry) MeterProvider() metric.MeterProvider {
	if t.mp == nil {
		return metricnoop.NewMeterProvider()
	}
	return t.mp
}

// Producer returns the self-telemetry metrics for the product reader when
// they inherit export.otlp, so one push carries the shared resource and its
// target_info; nil when self-telemetry metrics are inactive or go to an OTLP
// endpoint from the environment, which they push to on their own.
func (t *Telemetry) Producer() sdkmetric.Producer { return t.producer }

// TracerProvider returns the TracerProvider, a no-op one when traces are inactive.
func (t *Telemetry) TracerProvider() trace.TracerProvider {
	if t.tp == nil {
		return tracenoop.NewTracerProvider()
	}
	return t.tp
}

// LoggerProvider returns the LoggerProvider, a no-op one when logs are inactive.
func (t *Telemetry) LoggerProvider() log.LoggerProvider {
	if t.lp == nil {
		return lognoop.NewLoggerProvider()
	}
	return t.lp
}

// Meter returns the self-telemetry meter with the exporter's scope and version.
func (t *Telemetry) Meter() metric.Meter {
	return t.MeterProvider().Meter(ScopeName, metric.WithInstrumentationVersion(t.version))
}

// Tracer returns the tracer with the exporter's scope and version.
func (t *Telemetry) Tracer() trace.Tracer {
	return t.TracerProvider().Tracer(ScopeName, trace.WithInstrumentationVersion(t.version))
}

// Shutdown flushes and shuts down the active providers: metrics, traces, then
// logs, so records logged while shutting down still go out; then it stops the
// profiler, pushing the current CPU window.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	if t.mp != nil {
		if err := t.mp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter provider shutdown: %w", err))
		}
	}
	if t.tp != nil {
		if err := t.tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer provider shutdown: %w", err))
		}
	}
	if t.lp != nil {
		if err := t.lp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("logger provider shutdown: %w", err))
		}
	}
	if t.pr != nil {
		if err := t.pr.shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// resolveSignals applies the activation rule. Environment variables only turn
// signals off.
func resolveSignals(cfg config.Telemetry, getenv func(string) string, logger *slog.Logger) map[Signal]bool {
	active := map[Signal]bool{
		SignalMetrics:  cfg.Enabled && cfg.Metrics.Enabled,
		SignalTraces:   cfg.Enabled && cfg.Traces.Enabled,
		SignalLogs:     cfg.Enabled && cfg.Logs.Enabled,
		SignalProfiles: cfg.Enabled && cfg.Profiles.Enabled,
	}

	if v := strings.TrimSpace(getenv("OTEL_SDK_DISABLED")); strings.EqualFold(v, "true") {
		if cfg.Enabled {
			logger.Info("OTEL_SDK_DISABLED=true: self-telemetry is off; product metrics are not affected")
		}
		for s := range active {
			active[s] = false
		}
		return active
	} else if v != "" && !strings.EqualFold(v, "false") {
		logger.Warn("ignoring invalid OTEL_SDK_DISABLED value", "value", v)
	}

	// Profiles have no standard OTEL_*_EXPORTER variable.
	for _, s := range []Signal{SignalMetrics, SignalTraces, SignalLogs} {
		name := "OTEL_" + strings.ToUpper(string(s)) + "_EXPORTER"
		switch v := strings.TrimSpace(getenv(name)); strings.ToLower(v) {
		case "", "otlp":
		case "none":
			active[s] = false
		default:
			logger.Warn("unsupported exporter, using otlp", "variable", name, "value", v)
		}
	}
	return active
}

// ── OTLP destination ─────────────────────────────────────────────────────────

// destination is where one signal's OTLP exporter sends data.
type destination struct {
	// fromEnv: an OTLP endpoint is in the environment, so the SDK exporter is
	// built without options and reads every OTEL_EXPORTER_OTLP_* itself.
	fromEnv bool
	grpc    bool
	// Inherited from export.otlp when !fromEnv.
	endpointURL string // http
	endpoint    string // grpc host:port
	insecure    bool   // export.otlp.tls.insecure
	headers     map[string]string
}

func envSignal(getenv func(string) string, signal Signal, key string) string {
	if v := getenv("OTEL_EXPORTER_OTLP_" + strings.ToUpper(string(signal)) + "_" + key); v != "" {
		return v
	}
	return getenv("OTEL_EXPORTER_OTLP_" + key)
}

// resolveDestination picks the OTLP destination of signal: the environment
// when it sets an OTLP endpoint, export.otlp otherwise. Mixing the two would
// send export.otlp credentials to a backend the environment chose.
func resolveDestination(s Settings, signal Signal) (destination, error) {
	if envSignal(s.Getenv, signal, "ENDPOINT") != "" {
		d := destination{fromEnv: true}
		switch p := envSignal(s.Getenv, signal, "PROTOCOL"); p {
		case "grpc":
			d.grpc = true
		case "", "http/protobuf":
		default:
			s.Logger.Warn("unsupported OTLP protocol, using http/protobuf", "signal", signal, "value", p)
		}
		return d, nil
	}

	d := destination{insecure: s.OTLP.TLS.Insecure}
	// OTEL_EXPORTER_OTLP_*HEADERS, when set, replaces the inherited headers:
	// the SDK reads it when no header option is passed.
	if envSignal(s.Getenv, signal, "HEADERS") == "" {
		d.headers = s.OTLP.Headers
	}
	if s.OTLP.Protocol == "grpc" {
		d.grpc = true
		d.endpoint = s.OTLP.Endpoint
		return d, nil
	}
	u, err := url.Parse(s.OTLP.Endpoint)
	if err != nil || u.Host == "" {
		return d, fmt.Errorf("export.otlp.endpoint %q is not a URL", s.OTLP.Endpoint)
	}
	// export.otlp.endpoint is a base URL or the full metrics URL; either
	// way the signal path goes after the base, as for OTEL_EXPORTER_OTLP_ENDPOINT.
	base := strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/v1/metrics")
	u.Path = strings.TrimRight(base, "/") + "/v1/" + string(signal)
	d.endpointURL = u.String()
	return d, nil
}

// insecureTLS skips certificate verification for export.otlp.tls.insecure over HTTP.
func insecureTLS() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in, documented
}

// grpcTLS is the gRPC transport security when export.otlp.tls.insecure is off.
func grpcTLS() credentials.TransportCredentials {
	return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
}

func newTraceExporter(ctx context.Context, s Settings) (sdktrace.SpanExporter, error) {
	d, err := resolveDestination(s, SignalTraces)
	if err != nil {
		return nil, err
	}
	switch {
	case d.fromEnv && d.grpc:
		return otlptracegrpc.New(ctx)
	case d.fromEnv:
		return otlptracehttp.New(ctx)
	case d.grpc:
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(d.endpoint)}
		if d.insecure {
			opts = append(opts, otlptracegrpc.WithInsecure())
		} else {
			opts = append(opts, otlptracegrpc.WithTLSCredentials(grpcTLS()))
		}
		if len(d.headers) > 0 {
			opts = append(opts, otlptracegrpc.WithHeaders(d.headers))
		}
		return otlptracegrpc.New(ctx, opts...)
	default:
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(d.endpointURL)}
		if d.insecure {
			opts = append(opts, otlptracehttp.WithTLSClientConfig(insecureTLS()))
		}
		if len(d.headers) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(d.headers))
		}
		return otlptracehttp.New(ctx, opts...)
	}
}

func newLogExporter(ctx context.Context, s Settings) (sdklog.Exporter, error) {
	d, err := resolveDestination(s, SignalLogs)
	if err != nil {
		return nil, err
	}
	switch {
	case d.fromEnv && d.grpc:
		return otlploggrpc.New(ctx)
	case d.fromEnv:
		return otlploghttp.New(ctx)
	case d.grpc:
		opts := []otlploggrpc.Option{otlploggrpc.WithEndpoint(d.endpoint)}
		if d.insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		} else {
			opts = append(opts, otlploggrpc.WithTLSCredentials(grpcTLS()))
		}
		if len(d.headers) > 0 {
			opts = append(opts, otlploggrpc.WithHeaders(d.headers))
		}
		return otlploggrpc.New(ctx, opts...)
	default:
		opts := []otlploghttp.Option{otlploghttp.WithEndpointURL(d.endpointURL)}
		if d.insecure {
			opts = append(opts, otlploghttp.WithTLSClientConfig(insecureTLS()))
		}
		if len(d.headers) > 0 {
			opts = append(opts, otlploghttp.WithHeaders(d.headers))
		}
		return otlploghttp.New(ctx, opts...)
	}
}

func newMetricExporter(ctx context.Context, s Settings) (sdkmetric.Exporter, error) {
	d, err := resolveDestination(s, SignalMetrics)
	if err != nil {
		return nil, err
	}
	switch {
	case d.fromEnv && d.grpc:
		return otlpmetricgrpc.New(ctx)
	case d.fromEnv:
		return otlpmetrichttp.New(ctx)
	case d.grpc:
		opts := []otlpmetricgrpc.Option{otlpmetricgrpc.WithEndpoint(d.endpoint)}
		if d.insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		} else {
			opts = append(opts, otlpmetricgrpc.WithTLSCredentials(grpcTLS()))
		}
		if len(d.headers) > 0 {
			opts = append(opts, otlpmetricgrpc.WithHeaders(d.headers))
		}
		return otlpmetricgrpc.New(ctx, opts...)
	default:
		opts := []otlpmetrichttp.Option{otlpmetrichttp.WithEndpointURL(d.endpointURL)}
		if d.insecure {
			opts = append(opts, otlpmetrichttp.WithTLSClientConfig(insecureTLS()))
		}
		if len(d.headers) > 0 {
			opts = append(opts, otlpmetrichttp.WithHeaders(d.headers))
		}
		return otlpmetrichttp.New(ctx, opts...)
	}
}

// newMeterProvider builds the self-telemetry MeterProvider and its OTLP path.
// With an OTLP endpoint in the environment it gets a periodic OTLP reader
// (OTEL_METRIC_EXPORT_INTERVAL, default 60s). Inheriting export.otlp, it gets
// a manual reader returned as a Producer for the product reader instead: a
// second push of the shared resource would write target_info out of order
// with the product push. When the bridge is on, it also gets a Prometheus
// reader on the bridge's registry; the product reader already exposes
// target_info there, so this one does not. Every reader carries the Go
// runtime producer, the only source of go.schedule.duration.
func newMeterProvider(ctx context.Context, s Settings) (*sdkmetric.MeterProvider, sdkmetric.Producer, error) {
	d, err := resolveDestination(s, SignalMetrics)
	if err != nil {
		return nil, nil, err
	}
	opts := []sdkmetric.Option{sdkmetric.WithResource(s.Resource)}
	var producer sdkmetric.Producer
	var exp sdkmetric.Exporter
	if d.fromEnv {
		if exp, err = newMetricExporter(ctx, s); err != nil {
			return nil, nil, err
		}
		opts = append(opts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithProducer(newRuntimeProducer()))))
	} else {
		r := sdkmetric.NewManualReader(sdkmetric.WithProducer(newRuntimeProducer()))
		opts = append(opts, sdkmetric.WithReader(r))
		producer = readerProducer{r}
	}
	if s.PrometheusRegisterer != nil {
		prom, err := promexporter.New(
			promexporter.WithRegisterer(s.PrometheusRegisterer),
			promexporter.WithTranslationStrategy(otlptranslator.NoTranslation),
			promexporter.WithoutTargetInfo(),
			promexporter.WithProducer(newRuntimeProducer()),
		)
		if err != nil {
			err = fmt.Errorf("prometheus reader: %w", err)
			if exp != nil {
				err = errors.Join(err, exp.Shutdown(ctx))
			}
			return nil, nil, err
		}
		opts = append(opts, sdkmetric.WithReader(prom))
	}
	return sdkmetric.NewMeterProvider(opts...), producer, nil
}

// newRuntimeProducer returns the Go runtime producer (go.schedule.duration)
// reading the runtime on every Produce. With the default 15 s minimum read
// interval its data point keeps the time of the last read, up to 15 s old;
// in a push that also carries fresher data, Prometheus writes that resource's
// target_info at the oldest timestamp, behind the previous push's, and
// rejects it as out of order. A runtime/metrics read of one histogram is cheap.
func newRuntimeProducer() *runtime.Producer {
	return runtime.NewProducer(runtime.WithMinimumReadMemStatsInterval(time.Nanosecond).(runtime.ProducerOption))
}

// readerProducer exposes a manual reader's metrics as a Producer.
type readerProducer struct{ r *sdkmetric.ManualReader }

func (p readerProducer) Produce(ctx context.Context) ([]metricdata.ScopeMetrics, error) {
	var rm metricdata.ResourceMetrics
	if err := p.r.Collect(ctx, &rm); err != nil {
		return nil, err
	}
	return rm.ScopeMetrics, nil
}
