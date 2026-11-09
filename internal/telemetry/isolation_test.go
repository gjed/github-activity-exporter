package telemetry_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/exporter"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
	"github.com/gjed/github-activity-exporter/internal/telemetry"
)

// bodyReceiver is an OTLP/HTTP server that keeps the request bodies.
type bodyReceiver struct {
	*httptest.Server
	mu     sync.Mutex
	bodies [][]byte
}

func newBodyReceiver(t *testing.T) *bodyReceiver {
	t.Helper()
	r := &bodyReceiver{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, b)
		r.mu.Unlock()
	}))
	t.Cleanup(r.Close)
	return r
}

func (r *bodyReceiver) take() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.bodies
	r.bodies = nil
	return b
}

// TestProductMetricsIsolation runs the product pipeline next to self-telemetry
// for every toggle combination and OTEL_* toggle, and checks that the product
// OTLP push and /metrics are the same in all of them.
func TestProductMetricsIsolation(t *testing.T) {
	envs := []map[string]string{
		nil,
		{"OTEL_SDK_DISABLED": "true"},
		{"OTEL_TRACES_EXPORTER": "none", "OTEL_METRICS_EXPORTER": "none", "OTEL_LOGS_EXPORTER": "none"},
		{"OTEL_METRICS_EXPORTER": "none"},
		{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:1", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": "http://127.0.0.1:1/v1/metrics"},
	}
	ctx := context.Background()
	for mask := 0; mask < 32; mask++ {
		cfg := toggles(mask&1 != 0, mask&2 != 0, mask&4 != 0, mask&8 != 0, mask&16 != 0)
		for i, env := range envs {
			t.Run(fmt.Sprintf("toggles=%05b/env=%d", mask, i), func(t *testing.T) {
				clearEnv(t, env)
				rcv := newBodyReceiver(t)
				otlp := config.OTLP{Endpoint: rcv.URL + "/v1/metrics", Protocol: "http"}

				p, err := exporter.New(ctx, exporter.Config{
					OTLPEndpoint:      otlp.Endpoint,
					ExportInterval:    time.Hour,
					PrometheusEnabled: true,
					PrometheusAddr:    "127.0.0.1:0",
					Resource:          resource.Empty(),
					Version:           "1.2.3",
				}, nil)
				if err != nil {
					t.Fatalf("exporter.New: %v", err)
				}
				tel := newTelemetry(t, cfg, otlp, p.PrometheusRegisterer(), nil)

				created, _ := p.Meter().Int64Counter(sc.MetricGitHubPrCreated)
				created.Add(ctx, 1, metric.WithAttributes(attribute.String(sc.AttrExporterTarget, "t")))
				self, _ := tel.Meter().Int64Counter("test.self")
				self.Add(ctx, 1)

				if err := p.ForceFlush(ctx); err != nil {
					t.Fatalf("ForceFlush: %v", err)
				}
				pushes := rcv.take()
				if len(pushes) != 1 || !bytes.Contains(pushes[0], []byte(sc.MetricGitHubPrCreated)) || bytes.Contains(pushes[0], []byte("test.self")) {
					t.Errorf("product push = %d requests, want one carrying only product metrics", len(pushes))
				}

				families, err := p.PrometheusRegisterer().(prometheus.Gatherer).Gather()
				if err != nil {
					t.Fatalf("Gather: %v", err)
				}
				seen := map[string]bool{}
				for _, f := range families {
					seen[f.GetName()] = true
					if f.GetName() != sc.MetricGitHubPrCreated {
						continue
					}
					labels := map[string]string{}
					for _, l := range f.GetMetric()[0].GetLabel() {
						labels[l.GetName()] = l.GetValue()
					}
					if labels["otel_scope_name"] != telemetry.ScopeName || labels["otel_scope_version"] != "1.2.3" {
						t.Errorf("product scope labels = %v", labels)
					}
				}
				if !seen[sc.MetricGitHubPrCreated] || !seen["target_info"] {
					t.Errorf("/metrics lacks product metric or target_info: %v", seen)
				}
				if seen["test.self"] != tel.Active(telemetry.SignalMetrics) {
					t.Errorf("/metrics has self-telemetry metric = %v, want %v", seen["test.self"], tel.Active(telemetry.SignalMetrics))
				}

				if err := p.Shutdown(ctx); err != nil {
					t.Errorf("product Shutdown: %v", err)
				}
				_ = tel.Shutdown(ctx)
			})
		}
	}
}
