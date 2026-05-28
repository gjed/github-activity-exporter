//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gjed/renovate-exporter/internal/collector"
	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/discovery"
	"github.com/gjed/renovate-exporter/internal/semconv"
)

// TestCollectionLoop_MockData starts a full collection cycle using fixture data
// (no network calls) and asserts that the expected metric values are recorded.
func TestCollectionLoop_MockData(t *testing.T) {
	now := time.Now()
	mergedAt := now.Add(-1 * time.Hour)

	// ── PR fixture: 2 open, 1 merged (automerge, no reviews) ─────────────────
	prNodes := []collector.ExportedPRNode{
		{Number: 1, State: "OPEN", CreatedAt: now.Add(-48 * time.Hour)},
		{Number: 2, State: "OPEN", CreatedAt: now.Add(-24 * time.Hour)},
		{Number: 3, State: "MERGED", CreatedAt: now.Add(-5 * time.Hour), MergedAt: &mergedAt},
	}

	// ── Issue fixture: 1 open real issue, 1 dashboard ─────────────────────────
	openIssues := makeIssues(now)

	// ── Dashboard fixture ──────────────────────────────────────────────────────
	dashBody := `
## Awaiting Schedule
- [ ] foo
- [ ] bar

## Rate-Limited
- [x] baz

## Pending Approval

## Open
- [ ] quux
`

	// Build a test meter provider with a manual reader for synchronous collection.
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	meter := mp.Meter("integration-test")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── Build collectors ───────────────────────────────────────────────────────
	prColl, err := collector.NewPRCollector(
		collector.NewFixturePRClient(prNodes),
		config.PRFilters{},
		collector.PRCollectorConfig{MaxPRsPerRepo: 500, LookbackDays: 30},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}

	issColl, err := collector.NewIssueCollector(
		newFakeIssueListClient(openIssues),
		config.IssueFilters{},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewIssueCollector: %v", err)
	}

	dashIssues := makeDashboardIssueWithBody(dashBody)
	dashColl, err := collector.NewDashboardCollector(
		newFakeIssueListClient(dashIssues),
		"renovate[bot]",
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewDashboardCollector: %v", err)
	}

	// ── Build runner ───────────────────────────────────────────────────────────
	targetCfg := config.Target{Name: "test-target", Auth: config.Auth{Token: "x"}, Repos: []string{"org/repo"}}
	source := &fakeRepoSource{repos: []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}}

	runner, err := collector.NewRunner(targetCfg, source, collector.RunnerCollectors{
		PR:        prColl,
		Issue:     issColl,
		Dashboard: dashColl,
	}, meter, nil)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	// ── Single collection cycle ────────────────────────────────────────────────
	runner.RunOnce(ctx)

	// ── Collect metrics ────────────────────────────────────────────────────────
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("Collect metrics: %v", err)
	}

	// ── Assertions ─────────────────────────────────────────────────────────────

	// PR state counts
	prCounts := gaugeInt64Map(t, rm, semconv.MetricGitHubPrCount, semconv.AttrGitHubPrState)
	if prCounts["open"] != 2 {
		t.Errorf("github.pr.count{state=open} = %d, want 2", prCounts["open"])
	}

	// PR age gauge should exist (oldest open PR is ~48h)
	prAge := gaugeFloat64Values(t, rm, semconv.MetricGitHubPrAge)
	if len(prAge) == 0 {
		t.Error("no github.pr.age data points")
	}

	// PR close duration histogram should have 1 observation (merged PR)
	closeDur := histogramPoints(t, rm, semconv.MetricGitHubPrCloseDuration)
	if len(closeDur) == 0 || closeDur[0].Count != 1 {
		t.Errorf("github.pr.close.duration count = %d, want 1", func() uint64 {
			if len(closeDur) > 0 {
				return closeDur[0].Count
			}
			return 0
		}())
	}

	// Issue count
	issCounts := gaugeInt64Map(t, rm, semconv.MetricGitHubIssueCount, semconv.AttrGitHubIssueState)
	if issCounts["open"] != 1 {
		t.Errorf("github.issue.count{state=open} = %d, want 1", issCounts["open"])
	}

	// Dashboard queue
	dashSections := gaugeInt64Map(t, rm, semconv.MetricRenovateDashboardQueue, semconv.AttrRenovateDashboardSection)
	if dashSections[semconv.AttrRenovateDashboardSectionAwaitingSchedule] != 2 {
		t.Errorf("awaiting-schedule = %d, want 2", dashSections[semconv.AttrRenovateDashboardSectionAwaitingSchedule])
	}
	if dashSections[semconv.AttrRenovateDashboardSectionRateLimited] != 1 {
		t.Errorf("rate-limited = %d, want 1", dashSections[semconv.AttrRenovateDashboardSectionRateLimited])
	}

	// parse_error should be 0
	parseErr := gaugeInt64Values(t, rm, semconv.MetricRenovateDashboardParseError)
	for _, v := range parseErr {
		if v != 0 {
			t.Errorf("renovate.dashboard.parse_error = %d, want 0", v)
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func gaugeInt64Map(t *testing.T, rm metricdata.ResourceMetrics, name, attrKey string) map[string]int64 {
	t.Helper()
	key := attribute.Key(attrKey)
	m := make(map[string]int64)
	for _, sm := range rm.ScopeMetrics {
		for _, met := range sm.Metrics {
			if met.Name == name {
				if g, ok := met.Data.(metricdata.Gauge[int64]); ok {
					for _, dp := range g.DataPoints {
						if v, ok := dp.Attributes.Value(key); ok {
							m[v.AsString()] += dp.Value
						}
					}
				}
			}
		}
	}
	return m
}

func gaugeFloat64Values(t *testing.T, rm metricdata.ResourceMetrics, name string) []float64 {
	t.Helper()
	var vals []float64
	for _, sm := range rm.ScopeMetrics {
		for _, met := range sm.Metrics {
			if met.Name == name {
				if g, ok := met.Data.(metricdata.Gauge[float64]); ok {
					for _, dp := range g.DataPoints {
						vals = append(vals, dp.Value)
					}
				}
			}
		}
	}
	return vals
}

func gaugeInt64Values(t *testing.T, rm metricdata.ResourceMetrics, name string) []int64 {
	t.Helper()
	var vals []int64
	for _, sm := range rm.ScopeMetrics {
		for _, met := range sm.Metrics {
			if met.Name == name {
				if g, ok := met.Data.(metricdata.Gauge[int64]); ok {
					for _, dp := range g.DataPoints {
						vals = append(vals, dp.Value)
					}
				}
			}
		}
	}
	return vals
}

func histogramPoints(t *testing.T, rm metricdata.ResourceMetrics, name string) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, met := range sm.Metrics {
			if met.Name == name {
				if h, ok := met.Data.(metricdata.Histogram[float64]); ok {
					return h.DataPoints
				}
			}
		}
	}
	return nil
}
