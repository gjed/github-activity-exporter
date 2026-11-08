//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v93/github"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/exporter"
	ghclient "github.com/gjed/github-activity-exporter/internal/github"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
	"github.com/gjed/github-activity-exporter/internal/telemetry"
)

// Self-telemetry metrics (https://github.com/gjed/github-activity-exporter/issues/74):
// the OTLP and /metrics live-checks with the signal on and off.

// selfTelemetryMetrics are the go.* and http.* metrics expected with
// self-telemetry metrics active (go.memory.limit needs a memory limit).
var selfTelemetryMetrics = []string{
	"go.memory.used", "go.memory.allocated", "go.memory.allocations", "go.memory.gc.goal",
	"go.goroutine.count", "go.processor.limit", "go.config.gogc", "go.schedule.duration",
	"http.client.request.duration", "http.client.request.body.size",
}

// newSelfTelemetry builds the self-telemetry providers as main.go does, with
// metrics active or not, exporting OTLP to otlp.
func newSelfTelemetry(t *testing.T, active bool, otlp config.OTLP, reg prometheus.Registerer) *telemetry.Telemetry {
	t.Helper()
	// Keep the developer's environment from redirecting or disabling export.
	for _, k := range []string{"OTEL_SDK_DISABLED", "OTEL_METRICS_EXPORTER", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS"} {
		t.Setenv(k, "")
	}
	ctx := context.Background()
	res, err := telemetry.NewResource(ctx, "test")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}
	cfg := config.Telemetry{Enabled: active, Metrics: config.Signal{Enabled: true}}
	tel, err := telemetry.New(ctx, telemetry.Settings{
		Config: cfg, OTLP: otlp, Resource: res, Version: "test", PrometheusRegisterer: reg,
	})
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}
	return tel
}

// exerciseGitHub sends the exporter's kinds of GitHub request (credential
// check, REST issue listing, GraphQL) to a fake GitHub through the transport
// main.go builds: instrumented on tel's MeterProvider when metrics are active.
func exerciseGitHub(t *testing.T, tel *telemetry.Telemetry) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/graphql":
			_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"octocat"}}}`))
		case strings.HasSuffix(r.URL.Path, "/issues"):
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/repos/acme/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	var mp metric.MeterProvider
	if tel.Active(telemetry.SignalMetrics) {
		mp = tel.MeterProvider()
	}
	rt, err := ghclient.NewTransport(srv.URL, mp)
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	auth := ghclient.NewPATAuthenticatorWithBase("ghp_test", srv.URL, &http.Client{Transport: rt})
	c, err := ghclient.NewClient(auth, ghclient.WithBaseURL(srv.URL), ghclient.WithBaseTransport(rt))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx := context.Background()
	if err := auth.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if _, _, err := c.REST().Issues.ListByRepo(ctx, "acme", "widgets", &github.IssueListByRepoOptions{}); err != nil {
		t.Fatalf("ListByRepo: %v", err)
	}
	var q struct{ Viewer struct{ Login string } }
	if err := c.GraphQL().Query(ctx, &q, nil); err != nil {
		t.Fatalf("GraphQL: %v", err)
	}
	// A 404 on an unknown route: error.type, no url.template.
	_, _, _ = c.REST().Repositories.Get(ctx, "acme", "missing")
}

// selfTelemetryOTLP returns a live-check self hook that runs self-telemetry
// against the receiver, active or not, and shuts it down (which flushes).
func selfTelemetryOTLP(active bool) func(t *testing.T, addr string) {
	return func(t *testing.T, addr string) {
		tel := newSelfTelemetry(t, active, config.OTLP{Endpoint: addr, Protocol: "grpc", TLS: config.TLS{Insecure: true}}, nil)
		exerciseGitHub(t, tel)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := tel.Shutdown(ctx); err != nil {
			t.Fatalf("self-telemetry Shutdown: %v", err)
		}
	}
}

// TestLiveCheck_SelfTelemetryConformsToSchema pushes product metrics and,
// with self-telemetry metrics active, the runtime and GitHub HTTP client
// metrics, and asserts live-check sees them all with no violation.
func TestLiveCheck_SelfTelemetryConformsToSchema(t *testing.T) {
	out, err := runLiveCheckWithSelfTelemetry(t, emitConformingMetrics, selfTelemetryOTLP(true))
	if err != nil {
		t.Fatalf("weaver registry live-check failed (schema violation): %v", err)
	}
	stats, err := liveCheckStatistics(out)
	if err != nil {
		t.Fatalf("live-check report: %v", err)
	}
	for _, name := range selfTelemetryMetrics {
		if stats.SeenRegistryMetrics[name] == 0 {
			t.Errorf("live-check did not see %s (seen: %v)", name, stats.SeenRegistryMetrics)
		}
	}
	if len(stats.SeenNonRegistryMetrics) > 0 {
		t.Errorf("non-registry metrics seen: %v", stats.SeenNonRegistryMetrics)
	}
}

// TestLiveCheck_SelfTelemetryOff runs the same flow with self-telemetry
// metrics inactive and asserts live-check sees no go.* or http.* metric.
func TestLiveCheck_SelfTelemetryOff(t *testing.T) {
	out, err := runLiveCheckWithSelfTelemetry(t, emitConformingMetrics, selfTelemetryOTLP(false))
	if err != nil {
		t.Fatalf("weaver registry live-check failed (schema violation): %v", err)
	}
	stats, err := liveCheckStatistics(out)
	if err != nil {
		t.Fatalf("live-check report: %v", err)
	}
	requireNoSelfTelemetryNames(t, stats.SeenRegistryMetrics, stats.SeenNonRegistryMetrics)
}

// requireNoSelfTelemetryNames fails on a go.* or http.* name with a non-zero
// count; live-check lists every registry metric, unseen ones with 0.
func requireNoSelfTelemetryNames(t *testing.T, sets ...map[string]int) {
	t.Helper()
	for _, set := range sets {
		for name, n := range set {
			if n > 0 && (strings.HasPrefix(name, "go.") || strings.HasPrefix(name, "http.")) {
				t.Errorf("%s seen with self-telemetry metrics inactive", name)
			}
		}
	}
}

// scrapeSelfTelemetry records product metrics and runs self-telemetry, active
// or not, on an exporter with the Prometheus bridge enabled, and returns the
// parsed /metrics scrape.
func scrapeSelfTelemetry(t *testing.T, active bool) map[string]*dto.MetricFamily {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	p, err := exporter.New(context.Background(), exporter.Config{
		OTLPEndpoint:       "http://127.0.0.1:1", // unreachable: the 1h interval never fires
		CollectionInterval: time.Hour,
		PrometheusEnabled:  true,
		PrometheusAddr:     addr,
	}, nil)
	if err != nil {
		t.Fatalf("exporter.New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	})
	emitConformingMetrics(t, p)

	// Self-telemetry OTLP goes nowhere; only the bridge is scraped.
	tel := newSelfTelemetry(t, active, config.OTLP{Endpoint: "http://127.0.0.1:1", Protocol: "http"}, p.PrometheusRegisterer())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tel.Shutdown(ctx)
	})
	exerciseGitHub(t, tel)

	body := scrape(t, "http://"+addr+"/metrics", scrapeAccept["text"])
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse /metrics: %v\n%s", err, body)
	}
	return families
}

// TestPrometheusLiveCheck_SelfTelemetry scrapes /metrics with self-telemetry
// metrics active and asserts live-check sees the go.* and http.client.*
// families, each with its annotations.prometheus.type, and no violation.
func TestPrometheusLiveCheck_SelfTelemetry(t *testing.T) {
	out, err := runPrometheusLiveCheck(t, scrapeSelfTelemetry(t, true))
	if err != nil {
		t.Fatalf("weaver registry live-check failed (schema violation): %v", err)
	}
	stats, err := liveCheckStatistics(out)
	if err != nil {
		t.Fatalf("live-check report: %v", err)
	}
	for _, name := range selfTelemetryMetrics {
		if stats.SeenRegistryMetrics[name] == 0 {
			t.Errorf("live-check did not see %s (seen: %v)", name, stats.SeenRegistryMetrics)
		}
	}
	if len(stats.SeenNonRegistryMetrics) > 0 {
		t.Errorf("non-registry metrics seen: %v", stats.SeenNonRegistryMetrics)
	}
}

// TestPrometheusLiveCheck_SelfTelemetryOff asserts /metrics exposes no go.*
// or http.* family with self-telemetry metrics inactive.
func TestPrometheusLiveCheck_SelfTelemetryOff(t *testing.T) {
	families := scrapeSelfTelemetry(t, false)
	if _, ok := families[sc.MetricGitHubPrCount]; !ok {
		t.Fatalf("/metrics lacks the product metric %s: scrape is empty or broken", sc.MetricGitHubPrCount)
	}
	names := map[string]int{}
	for name := range families {
		names[name] = 1
	}
	requireNoSelfTelemetryNames(t, names)
}
