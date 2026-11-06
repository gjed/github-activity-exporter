package telemetry_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/otlptranslator"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/telemetry"
)

// otlpEnv lists every variable these tests read; each test starts with all
// of them empty so the developer's environment cannot leak in.
var otlpEnv = []string{
	"OTEL_SDK_DISABLED", "OTEL_TRACES_EXPORTER", "OTEL_METRICS_EXPORTER", "OTEL_LOGS_EXPORTER",
	"OTEL_TRACES_SAMPLER", "OTEL_TRACES_SAMPLER_ARG",
	"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
	"OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL",
	"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS", "OTEL_EXPORTER_OTLP_LOGS_HEADERS",
}

func clearEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range otlpEnv {
		t.Setenv(k, "")
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// restoreGlobals puts back the global providers New may replace.
func restoreGlobals(t *testing.T) {
	t.Helper()
	tp, lp := otel.GetTracerProvider(), otel.GetLoggerProvider()
	t.Cleanup(func() {
		// Setting a global to its current value makes the SDK log an error.
		if otel.GetTracerProvider() != tp {
			otel.SetTracerProvider(tp)
		}
		if otel.GetLoggerProvider() != lp {
			otel.SetLoggerProvider(lp)
		}
	})
}

func toggles(global, metrics, traces, logs, profiles bool) config.Telemetry {
	return config.Telemetry{
		Enabled:  global,
		Metrics:  config.Signal{Enabled: metrics},
		Traces:   config.Signal{Enabled: traces},
		Logs:     config.Signal{Enabled: logs},
		Profiles: config.Signal{Enabled: profiles},
	}
}

// receiver is an OTLP/HTTP server that records each request.
type receiver struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.reqs = append(r.reqs, req)
		r.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(r.Close)
	return r
}

// request returns the request to path, or nil.
func (r *receiver) request(path string) *http.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, req := range r.reqs {
		if req.URL.Path == path {
			return req
		}
	}
	return nil
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reqs)
}

func newTelemetry(t *testing.T, cfg config.Telemetry, otlp config.OTLP, reg prometheus.Registerer, logger *slog.Logger) *telemetry.Telemetry {
	t.Helper()
	restoreGlobals(t)
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	tel, err := telemetry.New(context.Background(), telemetry.Settings{
		Config:               cfg,
		OTLP:                 otlp,
		Resource:             resource.Empty(),
		Version:              "1.2.3",
		PrometheusRegisterer: reg,
		Logger:               logger,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tel
}

// emitAll records one item of every OTLP signal and shuts down, which flushes.
func emitAll(t *testing.T, tel *telemetry.Telemetry) {
	t.Helper()
	ctx := context.Background()
	ctr, err := tel.Meter().Int64Counter("test.counter")
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	ctr.Add(ctx, 1)
	_, span := tel.Tracer().Start(ctx, "test")
	span.End()
	var rec log.Record
	rec.SetBody(attribute.StringValue("test"))
	tel.LoggerProvider().Logger(telemetry.ScopeName).Emit(ctx, rec)
	if err := tel.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestActive_ToggleMatrix(t *testing.T) {
	all := []telemetry.Signal{telemetry.SignalMetrics, telemetry.SignalTraces, telemetry.SignalLogs, telemetry.SignalProfiles}
	tests := []struct {
		name string
		cfg  config.Telemetry
		env  map[string]string
		want []telemetry.Signal // active signals
	}{
		{name: "default config", cfg: toggles(false, true, true, true, true)},
		{name: "global off, signals on", cfg: toggles(false, true, true, true, true)},
		{name: "global on, all signals", cfg: toggles(true, true, true, true, true), want: all},
		{name: "global on, metrics off", cfg: toggles(true, false, true, true, true),
			want: []telemetry.Signal{telemetry.SignalTraces, telemetry.SignalLogs, telemetry.SignalProfiles}},
		{name: "global on, traces off", cfg: toggles(true, true, false, true, true),
			want: []telemetry.Signal{telemetry.SignalMetrics, telemetry.SignalLogs, telemetry.SignalProfiles}},
		{name: "global on, logs off", cfg: toggles(true, true, true, false, true),
			want: []telemetry.Signal{telemetry.SignalMetrics, telemetry.SignalTraces, telemetry.SignalProfiles}},
		{name: "global on, profiles off", cfg: toggles(true, true, true, true, false),
			want: []telemetry.Signal{telemetry.SignalMetrics, telemetry.SignalTraces, telemetry.SignalLogs}},
		{name: "global on, every signal off", cfg: toggles(true, false, false, false, false)},
		{name: "logs exporter none", cfg: toggles(true, true, true, true, true),
			env:  map[string]string{"OTEL_LOGS_EXPORTER": "none"},
			want: []telemetry.Signal{telemetry.SignalMetrics, telemetry.SignalTraces, telemetry.SignalProfiles}},
		{name: "every exporter none", cfg: toggles(true, true, true, true, true),
			env:  map[string]string{"OTEL_LOGS_EXPORTER": "none", "OTEL_TRACES_EXPORTER": "NONE", "OTEL_METRICS_EXPORTER": "none"},
			want: []telemetry.Signal{telemetry.SignalProfiles}},
		{name: "env cannot turn traces on", cfg: toggles(true, true, false, true, true),
			env:  map[string]string{"OTEL_TRACES_EXPORTER": "otlp"},
			want: []telemetry.Signal{telemetry.SignalMetrics, telemetry.SignalLogs, telemetry.SignalProfiles}},
		{name: "env cannot turn global on", cfg: toggles(false, true, true, true, true),
			env: map[string]string{"OTEL_TRACES_EXPORTER": "otlp", "OTEL_SDK_DISABLED": "false"}},
		{name: "unsupported exporter is otlp", cfg: toggles(true, true, true, true, true),
			env: map[string]string{"OTEL_METRICS_EXPORTER": "console"}, want: all},
		{name: "sdk disabled", cfg: toggles(true, true, true, true, true),
			env: map[string]string{"OTEL_SDK_DISABLED": "TRUE"}},
		{name: "sdk disabled false", cfg: toggles(true, true, true, true, true),
			env: map[string]string{"OTEL_SDK_DISABLED": "false"}, want: all},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv(t, tt.env)
			tel := newTelemetry(t, tt.cfg, config.OTLP{Endpoint: "http://127.0.0.1:1", Protocol: "http"}, nil, nil)
			t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
			want := map[telemetry.Signal]bool{}
			for _, s := range tt.want {
				want[s] = true
			}
			for _, s := range all {
				if got := tel.Active(s); got != want[s] {
					t.Errorf("Active(%s) = %v, want %v", s, got, want[s])
				}
			}
		})
	}
}

func TestNew_LogsEnvDecisions(t *testing.T) {
	clearEnv(t, map[string]string{"OTEL_SDK_DISABLED": "true"})
	var buf bytes.Buffer
	tel := newTelemetry(t, toggles(true, true, true, true, true), config.OTLP{Endpoint: "http://127.0.0.1:1", Protocol: "http"}, nil, slog.New(slog.NewTextHandler(&buf, nil)))
	_ = tel.Shutdown(context.Background())
	if !strings.Contains(buf.String(), "OTEL_SDK_DISABLED=true") {
		t.Errorf("no OTEL_SDK_DISABLED info log: %q", buf.String())
	}

	clearEnv(t, map[string]string{"OTEL_METRICS_EXPORTER": "console"})
	buf.Reset()
	tel = newTelemetry(t, toggles(true, true, true, true, true), config.OTLP{Endpoint: "http://127.0.0.1:1", Protocol: "http"}, nil, slog.New(slog.NewTextHandler(&buf, nil)))
	_ = tel.Shutdown(context.Background())
	if !strings.Contains(buf.String(), "OTEL_METRICS_EXPORTER") || !strings.Contains(buf.String(), "console") {
		t.Errorf("no unsupported exporter warning: %q", buf.String())
	}
}

func TestNew_InactiveCreatesNothing(t *testing.T) {
	clearEnv(t, nil)
	rcv := newReceiver(t)
	tracer, logger := otel.GetTracerProvider(), otel.GetLoggerProvider()
	for _, cfg := range []config.Telemetry{toggles(false, true, true, true, true), toggles(true, false, false, false, false)} {
		tel := newTelemetry(t, cfg, config.OTLP{Endpoint: rcv.URL, Protocol: "http"}, nil, nil)
		if otel.GetTracerProvider() != tracer || otel.GetLoggerProvider() != logger {
			t.Errorf("%+v: global providers replaced", cfg)
		}
		if _, ok := tel.MeterProvider().(*sdkmetric.MeterProvider); ok {
			t.Errorf("%+v: SDK MeterProvider built for inactive metrics", cfg)
		}
		if _, ok := tel.TracerProvider().(*sdktrace.TracerProvider); ok {
			t.Errorf("%+v: SDK TracerProvider built for inactive traces", cfg)
		}
		if _, ok := tel.LoggerProvider().(*sdklog.LoggerProvider); ok {
			t.Errorf("%+v: SDK LoggerProvider built for inactive logs", cfg)
		}
		emitAll(t, tel)
	}
	if n := rcv.count(); n != 0 {
		t.Errorf("inactive self-telemetry sent %d requests", n)
	}
}

func TestNew_ActiveSetsGlobals(t *testing.T) {
	clearEnv(t, nil)
	tel := newTelemetry(t, toggles(true, false, true, true, false), config.OTLP{Endpoint: "http://127.0.0.1:1", Protocol: "http"}, nil, nil)
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	if otel.GetTracerProvider() != tel.TracerProvider() {
		t.Error("global TracerProvider is not the self-telemetry one")
	}
	if otel.GetLoggerProvider() != tel.LoggerProvider() {
		t.Error("global LoggerProvider is not the self-telemetry one")
	}
}

func TestNew_InheritsExportOTLP(t *testing.T) {
	for _, path := range []string{"/otlp", "/otlp/", "/otlp/v1/metrics"} {
		t.Run(path, func(t *testing.T) {
			clearEnv(t, nil)
			rcv := newReceiver(t)
			tel := newTelemetry(t, toggles(true, true, true, true, true), config.OTLP{
				Endpoint: rcv.URL + path,
				Protocol: "http",
				Headers:  map[string]string{"authorization": "Bearer file"},
			}, nil, nil)
			emitAll(t, tel)
			for _, p := range []string{"/otlp/v1/traces", "/otlp/v1/metrics", "/otlp/v1/logs"} {
				req := rcv.request(p)
				if req == nil {
					t.Errorf("no request to %s", p)
					continue
				}
				if got := req.Header.Get("Authorization"); got != "Bearer file" {
					t.Errorf("%s: authorization = %q, want export.otlp header", p, got)
				}
			}
		})
	}
}

func TestNew_SignalEndpointWins(t *testing.T) {
	a, b := newReceiver(t), newReceiver(t)
	clearEnv(t, map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT":        a.URL,
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": b.URL + "/v1/traces",
	})
	tel := newTelemetry(t, toggles(true, true, true, true, true), config.OTLP{
		Endpoint: "http://127.0.0.1:1",
		Protocol: "http",
		Headers:  map[string]string{"authorization": "Bearer file"},
	}, nil, nil)
	emitAll(t, tel)
	if b.request("/v1/traces") == nil {
		t.Error("spans did not go to the traces endpoint")
	}
	if a.request("/v1/traces") != nil {
		t.Error("spans went to the generic endpoint")
	}
	for _, p := range []string{"/v1/logs", "/v1/metrics"} {
		req := a.request(p)
		if req == nil {
			t.Errorf("no request to generic endpoint %s", p)
			continue
		}
		if got := req.Header.Get("Authorization"); got != "" {
			t.Errorf("%s carries export.otlp header %q", p, got)
		}
	}
}

func TestNew_HeaderEnvReplacesInherited(t *testing.T) {
	rcv := newReceiver(t)
	clearEnv(t, map[string]string{"OTEL_EXPORTER_OTLP_HEADERS": "x-env=1"})
	tel := newTelemetry(t, toggles(true, false, true, false, false), config.OTLP{
		Endpoint: rcv.URL,
		Protocol: "http",
		Headers:  map[string]string{"authorization": "Bearer file"},
	}, nil, nil)
	emitAll(t, tel)
	req := rcv.request("/v1/traces")
	if req == nil {
		t.Fatal("no span export")
	}
	if req.Header.Get("X-Env") != "1" || req.Header.Get("Authorization") != "" {
		t.Errorf("headers = %v, want x-env only", req.Header)
	}
}

func TestNew_SamplerFromEnv(t *testing.T) {
	for _, tt := range []struct {
		env     map[string]string
		sampled bool
	}{
		{env: nil, sampled: true},
		{env: map[string]string{"OTEL_TRACES_SAMPLER": "traceidratio", "OTEL_TRACES_SAMPLER_ARG": "0"}, sampled: false},
	} {
		clearEnv(t, tt.env)
		tel := newTelemetry(t, toggles(true, false, true, false, false), config.OTLP{Endpoint: "http://127.0.0.1:1", Protocol: "http"}, nil, nil)
		_, span := tel.Tracer().Start(context.Background(), "x")
		if got := span.SpanContext().IsSampled(); got != tt.sampled {
			t.Errorf("env %v: sampled = %v, want %v", tt.env, got, tt.sampled)
		}
		span.End()
		_ = tel.Shutdown(context.Background())
	}
}

func TestNew_PrometheusBridge(t *testing.T) {
	clearEnv(t, nil)
	reg := prometheus.NewRegistry()
	// The product reader, as internal/exporter builds it, owns target_info.
	product, err := promexporter.New(promexporter.WithRegisterer(reg), promexporter.WithTranslationStrategy(otlptranslator.NoTranslation))
	if err != nil {
		t.Fatalf("product reader: %v", err)
	}
	productMP := sdkmetric.NewMeterProvider(sdkmetric.WithReader(product), sdkmetric.WithResource(resource.Empty()))
	pc, _ := productMP.Meter("p").Int64Counter("product.counter")
	pc.Add(context.Background(), 1)

	tel := newTelemetry(t, toggles(true, true, false, false, false), config.OTLP{Endpoint: "http://127.0.0.1:1", Protocol: "http"}, reg, nil)
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
	sc, _ := tel.Meter().Int64Counter("self.counter")
	sc.Add(context.Background(), 1)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	seen := map[string]int{}
	for _, f := range families {
		seen[f.GetName()]++
	}
	for _, name := range []string{"product.counter", "self.counter", "target_info"} {
		if seen[name] != 1 {
			t.Errorf("family %s seen %d times, want 1 (families: %v)", name, seen[name], seen)
		}
	}
}
