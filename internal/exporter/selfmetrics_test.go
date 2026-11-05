package exporter_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gjed/github-activity-exporter/internal/exporter"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
)

func TestSelfMetrics_RecordAPIError_Endpoint(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	sm, err := exporter.NewSelfMetrics()
	if err != nil {
		t.Fatalf("NewSelfMetrics: %v", err)
	}
	sm.RecordAPIError(context.Background(), "org/repo", sc.AttrGitHubApiEndpointGraphQL)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != sc.MetricGitHubExporterApiErrors {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) != 1 {
				t.Fatalf("unexpected data %#v", m.Data)
			}
			attrs := sum.DataPoints[0].Attributes
			if v, _ := attrs.Value(attribute.Key(sc.AttrGitHubApiEndpoint)); v.AsString() != sc.AttrGitHubApiEndpointGraphQL {
				t.Errorf("%s = %q, want %q", sc.AttrGitHubApiEndpoint, v.AsString(), sc.AttrGitHubApiEndpointGraphQL)
			}
			if v, _ := attrs.Value(attribute.Key(sc.AttrExporterTarget)); v.AsString() != "org/repo" {
				t.Errorf("%s = %q, want org/repo", sc.AttrExporterTarget, v.AsString())
			}
			return
		}
	}
	t.Fatalf("metric %s not collected", sc.MetricGitHubExporterApiErrors)
}

// TestSelfMetrics_RecordRateLimit_ZeroResetKeepsLast records a rate limit
// state with a reset time, then one without (no X-RateLimit-Reset header),
// and asserts remaining takes the second value while reset keeps the first.
func TestSelfMetrics_RecordRateLimit_ZeroResetKeepsLast(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	sm, err := exporter.NewSelfMetrics()
	if err != nil {
		t.Fatalf("NewSelfMetrics: %v", err)
	}
	resetAt := time.Unix(1_800_000_000, 0)
	sm.RecordRateLimit(context.Background(), "org/repo", 4999, resetAt)
	sm.RecordRateLimit(context.Background(), "org/repo", 4998, time.Time{})

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := map[string]int64{
		sc.MetricGitHubExporterApiRateLimitRemaining: 4998,
		sc.MetricGitHubExporterApiRateLimitReset:     resetAt.Unix(),
	}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			w, ok := want[m.Name]
			if !ok {
				continue
			}
			delete(want, m.Name)
			g, ok := m.Data.(metricdata.Gauge[int64])
			if !ok || len(g.DataPoints) != 1 {
				t.Fatalf("%s: unexpected data %#v", m.Name, m.Data)
			}
			dp := g.DataPoints[0]
			if dp.Value != w {
				t.Errorf("%s = %d, want %d", m.Name, dp.Value, w)
			}
			if v, _ := dp.Attributes.Value(attribute.Key(sc.AttrExporterTarget)); v.AsString() != "org/repo" {
				t.Errorf("%s: %s = %q, want org/repo", m.Name, sc.AttrExporterTarget, v.AsString())
			}
		}
	}
	for name := range want {
		t.Errorf("metric %s not collected", name)
	}
}
