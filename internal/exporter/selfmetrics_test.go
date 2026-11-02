package exporter_test

import (
	"context"
	"testing"

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
