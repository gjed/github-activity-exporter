package collector_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gjed/renovate-exporter/internal/collector"
	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/discovery"
	"github.com/gjed/renovate-exporter/internal/semconv"
)

// fakePRGraphQL is a fake GraphQL client that returns pre-baked PR fixture data.
type fakePRGraphQL struct {
	// pages maps "owner/name" to pages of PR nodes.
	pages map[string][][]collector.ExportedPRNode
	calls map[string]int
}

func (f *fakePRGraphQL) Query(ctx context.Context, q interface{}, variables map[string]interface{}) error {
	owner := string(variables["owner"].(interface{ String() string }).String())
	name := string(variables["name"].(interface{ String() string }).String())
	_ = owner
	_ = name
	_ = ctx
	_ = q
	_ = variables
	// stub: handled by stub implementation below
	return nil
}

// fakePRQuery is a simpler fake that holds all pages for a repo.
type fakePRGraphQLSimple struct {
	nodes []collector.ExportedPRNode
}

func (f *fakePRGraphQLSimple) Query(_ context.Context, q interface{}, _ map[string]interface{}) error {
	return collector.InjectPRQueryResult(q, f.nodes, false)
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptrStr(s string) *string { return &s }
func ptrTime(t time.Time) *time.Time { return &t }

func newTestMeterProvider() (*metric.MeterProvider, *metric.ManualReader) {
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	return mp, reader
}

func collectMetrics(t *testing.T, reader *metric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	return rm
}

func findGaugeInt(t *testing.T, rm metricdata.ResourceMetrics, name string) []metricdata.DataPoint[int64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				g, ok := m.Data.(metricdata.Gauge[int64])
				if !ok {
					t.Fatalf("metric %q is not a Gauge[int64]", name)
				}
				return g.DataPoints
			}
		}
	}
	return nil
}

func findGaugeFloat(t *testing.T, rm metricdata.ResourceMetrics, name string) []metricdata.DataPoint[float64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				g, ok := m.Data.(metricdata.Gauge[float64])
				if !ok {
					t.Fatalf("metric %q is not a Gauge[float64]", name)
				}
				return g.DataPoints
			}
		}
	}
	return nil
}

func findHistogram(t *testing.T, rm metricdata.ResourceMetrics, name string) []metricdata.HistogramDataPoint[float64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				h, ok := m.Data.(metricdata.Histogram[float64])
				if !ok {
					t.Fatalf("metric %q is not a Histogram", name)
				}
				return h.DataPoints
			}
		}
	}
	return nil
}

func sumInt64DataPoints(dps []metricdata.DataPoint[int64]) int64 {
	var total int64
	for _, dp := range dps {
		total += dp.Value
	}
	return total
}

func TestPRCollector_StateCount(t *testing.T) {
	now := time.Now()

	nodes := []collector.ExportedPRNode{
		{Number: 1, State: "OPEN", CreatedAt: now.Add(-24 * time.Hour)},
		{Number: 2, State: "OPEN", CreatedAt: now.Add(-48 * time.Hour)},
		{Number: 3, State: "MERGED", CreatedAt: now.Add(-72 * time.Hour), MergedAt: ptrTime(now.Add(-1 * time.Hour))},
		{Number: 4, State: "CLOSED", CreatedAt: now.Add(-96 * time.Hour), ClosedAt: ptrTime(now.Add(-2 * time.Hour))},
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewPRCollector(
		collector.NewFixturePRClient(nodes),
		config.PRFilters{},
		collector.PRCollectorConfig{MaxPRsPerRepo: 500, LookbackDays: 30},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}

	repos := []discovery.Repo{{Owner: "myorg", Name: "myrepo", FullName: "myorg/myrepo"}}
	result, err := coll.Collect(context.Background(), "test-target", repos)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	_, err = mp.Meter("test").RegisterCallback(coll.ObservableRegistration(result), coll.ObservableInstruments()...)
	if err != nil {
		t.Fatalf("RegisterCallback: %v", err)
	}

	rm := collectMetrics(t, reader)
	dps := findGaugeInt(t, rm, semconv.MetricGitHubPrCount)

	// Count "open" state points
	var openCount int64
	for _, dp := range dps {
		if v, ok := dp.Attributes.Value(semconv.AttrGitHubPrState); ok && v.AsString() == "open" {
			openCount += dp.Value
		}
	}
	if openCount != 2 {
		t.Errorf("open PR count = %d, want 2", openCount)
	}
}

func TestPRCollector_OldestOpenAge(t *testing.T) {
	// PR created 2 days ago should be the oldest open
	now := time.Now()
	older := now.Add(-48 * time.Hour)

	nodes := []collector.ExportedPRNode{
		{Number: 1, State: "OPEN", CreatedAt: now.Add(-24 * time.Hour)},
		{Number: 2, State: "OPEN", CreatedAt: older},
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewPRCollector(
		collector.NewFixturePRClient(nodes),
		config.PRFilters{},
		collector.PRCollectorConfig{MaxPRsPerRepo: 500, LookbackDays: 30},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}

	repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
	result, err := coll.Collect(context.Background(), "tgt", repos)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	_, err = mp.Meter("test").RegisterCallback(coll.ObservableRegistration(result), coll.ObservableInstruments()...)
	if err != nil {
		t.Fatalf("RegisterCallback: %v", err)
	}

	rm := collectMetrics(t, reader)
	dps := findGaugeFloat(t, rm, semconv.MetricGitHubPrAge)
	if len(dps) == 0 {
		t.Fatal("no github.pr.age data points")
	}

	// Age should be ~48h in seconds (±60s tolerance)
	wantMin := (48*time.Hour - 60*time.Second).Seconds()
	if dps[0].Value < wantMin {
		t.Errorf("pr.age = %v, want >= %v", dps[0].Value, wantMin)
	}
}

func TestPRCollector_CloseDuration(t *testing.T) {
	now := time.Now()
	// Merged 1h ago, was open for 2h → duration ~7200s
	mergedAt := now.Add(-1 * time.Hour)
	nodes := []collector.ExportedPRNode{
		{
			Number:    1,
			State:     "MERGED",
			CreatedAt: now.Add(-3 * time.Hour),
			MergedAt:  &mergedAt,
		},
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewPRCollector(
		collector.NewFixturePRClient(nodes),
		config.PRFilters{},
		collector.PRCollectorConfig{MaxPRsPerRepo: 500, LookbackDays: 30},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}

	repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
	if _, err := coll.Collect(context.Background(), "tgt", repos); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	rm := collectMetrics(t, reader)
	dps := findHistogram(t, rm, semconv.MetricGitHubPrCloseDuration)
	if len(dps) == 0 {
		t.Fatal("no github.pr.close.duration data points")
	}
	if dps[0].Count != 1 {
		t.Errorf("close duration count = %d, want 1", dps[0].Count)
	}
}

func TestPRCollector_AutomergeDetection(t *testing.T) {
	now := time.Now()
	mergedAt := now.Add(-1 * time.Hour)

	tests := []struct {
		name         string
		reviews      []string // review states
		wantCounted  bool
	}{
		{
			name:        "no reviews → automerge",
			reviews:     nil,
			wantCounted: true,
		},
		{
			name:        "approved review → not automerge",
			reviews:     []string{"APPROVED"},
			wantCounted: false,
		},
		{
			name:        "commented only → automerge",
			reviews:     []string{"COMMENTED"},
			wantCounted: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node := collector.ExportedPRNode{
				Number:    1,
				State:     "MERGED",
				CreatedAt: now.Add(-5 * time.Hour),
				MergedAt:  &mergedAt,
				Reviews:   tc.reviews,
			}

			mp, reader := newTestMeterProvider()
			meter := mp.Meter("test")

			coll, err := collector.NewPRCollector(
				collector.NewFixturePRClient([]collector.ExportedPRNode{node}),
				config.PRFilters{},
				collector.PRCollectorConfig{MaxPRsPerRepo: 500, LookbackDays: 30},
				meter,
				nil,
			)
			if err != nil {
				t.Fatalf("NewPRCollector: %v", err)
			}

			repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
			if _, err := coll.Collect(context.Background(), "tgt", repos); err != nil {
				t.Fatalf("Collect: %v", err)
			}

			rm := collectMetrics(t, reader)
			var automergedCount int64
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if m.Name == semconv.MetricGitHubPrAutomerged {
						if d, ok := m.Data.(metricdata.Sum[int64]); ok {
							for _, dp := range d.DataPoints {
								automergedCount += dp.Value
							}
						}
					}
				}
			}

			if tc.wantCounted && automergedCount == 0 {
				t.Error("expected automerge to be counted, got 0")
			}
			if !tc.wantCounted && automergedCount > 0 {
				t.Errorf("expected no automerge, got %d", automergedCount)
			}
		})
	}
}

func TestPRCollector_LabelFilter(t *testing.T) {
	now := time.Now()
	nodes := []collector.ExportedPRNode{
		{Number: 1, State: "OPEN", CreatedAt: now.Add(-1 * time.Hour), Labels: []string{"renovate", "dependencies"}},
		{Number: 2, State: "OPEN", CreatedAt: now.Add(-2 * time.Hour), Labels: []string{"bug"}},
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	// Include only "renovate" label
	coll, err := collector.NewPRCollector(
		collector.NewFixturePRClient(nodes),
		config.PRFilters{IncludeLabels: []string{"renovate"}},
		collector.PRCollectorConfig{MaxPRsPerRepo: 500, LookbackDays: 30},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}

	repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
	result, err := coll.Collect(context.Background(), "tgt", repos)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	_, err = mp.Meter("test").RegisterCallback(coll.ObservableRegistration(result), coll.ObservableInstruments()...)
	if err != nil {
		t.Fatalf("RegisterCallback: %v", err)
	}

	rm := collectMetrics(t, reader)
	dps := findGaugeInt(t, rm, semconv.MetricGitHubPrCount)

	// Only 1 PR (number 2 with label "bug" should be filtered out)
	var stateTotal int64
	for _, dp := range dps {
		if _, ok := dp.Attributes.Value(semconv.AttrGitHubPrState); ok {
			stateTotal += dp.Value
		}
	}
	if stateTotal != 1 {
		t.Errorf("state-keyed PR count = %d, want 1 (only renovate-labeled PR)", stateTotal)
	}
}

func TestPRCollector_ReviewStatus(t *testing.T) {
	now := time.Now()
	approved := "APPROVED"
	changesReq := "CHANGES_REQUESTED"

	nodes := []collector.ExportedPRNode{
		{Number: 1, State: "OPEN", CreatedAt: now.Add(-1 * time.Hour), ReviewDecision: &approved},
		{Number: 2, State: "OPEN", CreatedAt: now.Add(-2 * time.Hour), ReviewDecision: &changesReq},
		{Number: 3, State: "OPEN", CreatedAt: now.Add(-3 * time.Hour)}, // nil → none
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewPRCollector(
		collector.NewFixturePRClient(nodes),
		config.PRFilters{},
		collector.PRCollectorConfig{MaxPRsPerRepo: 500, LookbackDays: 30},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}

	repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
	result, err := coll.Collect(context.Background(), "tgt", repos)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	_, err = mp.Meter("test").RegisterCallback(coll.ObservableRegistration(result), coll.ObservableInstruments()...)
	if err != nil {
		t.Fatalf("RegisterCallback: %v", err)
	}

	rm := collectMetrics(t, reader)
	dps := findGaugeInt(t, rm, semconv.MetricGitHubPrReviewStatus)

	statusMap := make(map[string]int64)
	for _, dp := range dps {
		if v, ok := dp.Attributes.Value(semconv.AttrGitHubPrReviewStatus); ok {
			statusMap[v.AsString()] += dp.Value
		}
	}

	tests := []struct {
		status string
		want   int64
	}{
		{semconv.AttrGitHubPrReviewStatusApproved, 1},
		{semconv.AttrGitHubPrReviewStatusChangesRequested, 1},
		{semconv.AttrGitHubPrReviewStatusNone, 1},
	}
	for _, tc := range tests {
		if got := statusMap[tc.status]; got != tc.want {
			t.Errorf("review_status[%q] = %d, want %d", tc.status, got, tc.want)
		}
	}
}
