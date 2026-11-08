package exporter_test

import (
	"context"
	"slices"
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

	sm, err := exporter.NewSelfMetrics(otel.GetMeterProvider().Meter("test"))
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

	sm, err := exporter.NewSelfMetrics(otel.GetMeterProvider().Meter("test"))
	if err != nil {
		t.Fatalf("NewSelfMetrics: %v", err)
	}
	resetAt := time.Unix(1_800_000_000, 0)
	core := sc.AttrGitHubApiRateLimitResourceCore
	sm.RecordRateLimit(context.Background(), "org/repo", core, 4999, resetAt)
	sm.RecordRateLimit(context.Background(), "org/repo", core, 4998, time.Time{})

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

// TestSelfMetrics_RecordRateLimit_SeriesPerPool records core and graphql
// states interleaved and asserts each gauge keeps one series per pool with
// that pool's last value: an update to one pool never overwrites another.
func TestSelfMetrics_RecordRateLimit_SeriesPerPool(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	sm, err := exporter.NewSelfMetrics(otel.GetMeterProvider().Meter("test"))
	if err != nil {
		t.Fatalf("NewSelfMetrics: %v", err)
	}
	core, gql := sc.AttrGitHubApiRateLimitResourceCore, sc.AttrGitHubApiRateLimitResourceGraphQL
	coreReset, gqlReset := time.Unix(1_800_000_000, 0), time.Unix(1_800_000_600, 0)
	ctx := context.Background()
	sm.RecordRateLimit(ctx, "org/repo", core, 4999, coreReset)
	sm.RecordRateLimit(ctx, "org/repo", gql, 4500, gqlReset)
	sm.RecordRateLimit(ctx, "org/repo", core, 4998, time.Time{})

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := map[string]map[string]int64{
		sc.MetricGitHubExporterApiRateLimitRemaining: {core: 4998, gql: 4500},
		sc.MetricGitHubExporterApiRateLimitReset:     {core: coreReset.Unix(), gql: gqlReset.Unix()},
	}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			w, ok := want[m.Name]
			if !ok {
				continue
			}
			delete(want, m.Name)
			g, ok := m.Data.(metricdata.Gauge[int64])
			if !ok {
				t.Fatalf("%s: unexpected data %#v", m.Name, m.Data)
			}
			got := map[string]int64{}
			for _, dp := range g.DataPoints {
				if v, _ := dp.Attributes.Value(attribute.Key(sc.AttrExporterTarget)); v.AsString() != "org/repo" {
					t.Errorf("%s: %s = %q, want org/repo", m.Name, sc.AttrExporterTarget, v.AsString())
				}
				pool, _ := dp.Attributes.Value(attribute.Key(sc.AttrGitHubApiRateLimitResource))
				got[pool.AsString()] = dp.Value
			}
			if len(got) != len(w) || got[core] != w[core] || got[gql] != w[gql] {
				t.Errorf("%s series = %v, want %v", m.Name, got, w)
			}
		}
	}
	for name := range want {
		t.Errorf("metric %s not collected", name)
	}
}

// TestSelfMetrics_InitOverruns_ExportsZero asserts the overrun series of a
// target exists with value 0 before any overrun.
func TestSelfMetrics_InitOverruns_ExportsZero(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	sm, err := exporter.NewSelfMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test"))
	if err != nil {
		t.Fatalf("NewSelfMetrics: %v", err)
	}
	sm.InitOverruns(context.Background(), "tgt")

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != sc.MetricGitHubExporterScrapeOverruns {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) != 1 {
				t.Fatalf("unexpected data %#v", m.Data)
			}
			dp := sum.DataPoints[0]
			if dp.Value != 0 {
				t.Errorf("value = %d, want 0", dp.Value)
			}
			if v, _ := dp.Attributes.Value(attribute.Key(sc.AttrExporterTarget)); v.AsString() != "tgt" {
				t.Errorf("%s = %q, want tgt", sc.AttrExporterTarget, v.AsString())
			}
			return
		}
	}
	t.Fatalf("metric %s not collected", sc.MetricGitHubExporterScrapeOverruns)
}

// TestSelfMetrics_ScrapeDurationBuckets asserts the bucket boundaries and that
// a 1040 s cycle lands in the 1800 bucket, not +Inf.
func TestSelfMetrics_ScrapeDurationBuckets(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	sm, err := exporter.NewSelfMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test"))
	if err != nil {
		t.Fatalf("NewSelfMetrics: %v", err)
	}
	sm.RecordScrapeDuration(context.Background(), "tgt", time.Now().Add(-1040*time.Second))

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	want := []float64{1, 5, 10, 30, 60, 120, 300, 600, 900, 1800, 3600}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != sc.MetricGitHubExporterScrapeDuration {
				continue
			}
			h, ok := m.Data.(metricdata.Histogram[float64])
			if !ok || len(h.DataPoints) != 1 {
				t.Fatalf("unexpected data %#v", m.Data)
			}
			dp := h.DataPoints[0]
			if !slices.Equal(dp.Bounds, want) {
				t.Fatalf("bounds = %v, want %v", dp.Bounds, want)
			}
			// BucketCounts[i] counts values in (Bounds[i-1], Bounds[i]].
			i := slices.Index(dp.Bounds, 1800)
			if dp.BucketCounts[i] != 1 {
				t.Errorf("bucket counts = %v, want the observation in (900, 1800]", dp.BucketCounts)
			}
			return
		}
	}
	t.Fatalf("metric %s not collected", sc.MetricGitHubExporterScrapeDuration)
}
