package collector_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/semconv"
)

// sinceAll returns a lastCollectedAt map giving every repo the same timestamp.
func sinceAll(repos []discovery.Repo, t time.Time) map[string]time.Time {
	m := make(map[string]time.Time, len(repos))
	for _, r := range repos {
		m[r.FullName] = t
	}
	return m
}

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

// findGaugeInt returns int64 data points for metrics that are either
// Gauge[int64] (observable gauges) or Sum[int64] (observable up/down counters).
func findGaugeInt(t *testing.T, rm metricdata.ResourceMetrics, name string) []metricdata.DataPoint[int64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				if g, ok := m.Data.(metricdata.Gauge[int64]); ok {
					return g.DataPoints
				}
				if s, ok := m.Data.(metricdata.Sum[int64]); ok {
					return s.DataPoints
				}
				t.Fatalf("metric %q is neither Gauge[int64] nor Sum[int64]", name)
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
		collector.PRCollectorConfig{LookbackDays: 30},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}

	repos := []discovery.Repo{{Owner: "myorg", Name: "myrepo", FullName: "myorg/myrepo"}}
	result, err := coll.Collect(context.Background(), "test-target", repos, sinceAll(repos, time.Now().Add(-time.Hour)), time.Now())
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
	for _, dp := range dps {
		if v, _ := dp.Attributes.Value(semconv.AttrGitHubPrState); v.AsString() == "closed" || v.AsString() == "merged" {
			t.Errorf("github.pr.count has a %s data point; want open/draft only", v.AsString())
		}
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
		collector.PRCollectorConfig{LookbackDays: 30},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}

	repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
	result, err := coll.Collect(context.Background(), "tgt", repos, sinceAll(repos, time.Now().Add(-2*time.Hour)), time.Now())
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
		collector.PRCollectorConfig{LookbackDays: 30},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}

	repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
	if _, err := coll.Collect(context.Background(), "tgt", repos, sinceAll(repos, time.Now().Add(-2*time.Hour)), time.Now()); err != nil {
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
		name        string
		reviews     []string // review states
		wantCounted bool
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
				collector.PRCollectorConfig{LookbackDays: 30},
				meter,
				nil,
			)
			if err != nil {
				t.Fatalf("NewPRCollector: %v", err)
			}

			repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
			if _, err := coll.Collect(context.Background(), "tgt", repos, sinceAll(repos, time.Now().Add(-2*time.Hour)), time.Now()); err != nil {
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
		collector.PRCollectorConfig{LookbackDays: 30},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}

	repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
	result, err := coll.Collect(context.Background(), "tgt", repos, sinceAll(repos, time.Now().Add(-2*time.Hour)), time.Now())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	_, err = mp.Meter("test").RegisterCallback(coll.ObservableRegistration(result), coll.ObservableInstruments()...)
	if err != nil {
		t.Fatalf("RegisterCallback: %v", err)
	}

	rm := collectMetrics(t, reader)
	dps := findGaugeInt(t, rm, semconv.MetricGitHubPrCount)

	// Only PR #1 (renovate-labeled) passes the filter.
	// pr.count emits: (open, ""), (open, "renovate"), (open, "dependencies").
	// The state-only point (no label attribute) should be 1.
	stateKey := attribute.Key(semconv.AttrGitHubPrState)
	labelKey := attribute.Key(semconv.AttrGitHubPrLabel)
	var stateOnlyCount int64
	for _, dp := range dps {
		_, hasState := dp.Attributes.Value(stateKey)
		_, hasLabel := dp.Attributes.Value(labelKey)
		if hasState && !hasLabel {
			stateOnlyCount += dp.Value
		}
	}
	if stateOnlyCount != 1 {
		t.Errorf("state-only PR count = %d, want 1 (only renovate-labeled PR passed filter)", stateOnlyCount)
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
		collector.PRCollectorConfig{LookbackDays: 30},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}

	repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
	result, err := coll.Collect(context.Background(), "tgt", repos, sinceAll(repos, time.Now().Add(-2*time.Hour)), time.Now())
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

// sumCounter sums every data point of an int64 counter.
func sumCounter(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	var total int64
	for _, dp := range findGaugeInt(t, rm, name) {
		total += dp.Value
	}
	return total
}

// counterPoints returns how many data points an int64 counter has.
func counterPoints(t *testing.T, rm metricdata.ResourceMetrics, name string) int {
	t.Helper()
	return len(findGaugeInt(t, rm, name))
}

type eventTotals struct{ created, merged, closed, automerged int64 }

func readEventTotals(t *testing.T, reader *metric.ManualReader) eventTotals {
	t.Helper()
	rm := collectMetrics(t, reader)
	return eventTotals{
		created:    sumCounter(t, rm, semconv.MetricGitHubPrCreated),
		merged:     sumCounter(t, rm, semconv.MetricGitHubPrMerged),
		closed:     sumCounter(t, rm, semconv.MetricGitHubPrClosed),
		automerged: sumCounter(t, rm, semconv.MetricGitHubPrAutomerged),
	}
}

func newCollector(t *testing.T, gql collector.GraphQLClient, filters config.PRFilters) (*collector.PRCollector, *metric.ManualReader, *metric.MeterProvider) {
	t.Helper()
	mp, reader := newTestMeterProvider()
	coll, err := collector.NewPRCollector(gql, filters, collector.PRCollectorConfig{LookbackDays: 30}, mp.Meter("test"), nil)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}
	return coll, reader, mp
}

var oneRepo = []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}

// A repo with more than 500 open PRs is read to the end: no truncation.
func TestPRCollector_OpenScanNoTruncation(t *testing.T) {
	now := time.Now()
	nodes := make([]collector.ExportedPRNode, 0, 1250)
	for i := range 1200 {
		nodes = append(nodes, collector.ExportedPRNode{Number: i + 1, State: "OPEN", CreatedAt: now.Add(-time.Duration(i+1) * time.Minute)})
	}
	for i := range 50 {
		nodes = append(nodes, collector.ExportedPRNode{Number: 2000 + i, State: "OPEN", IsDraft: true, CreatedAt: now.Add(-time.Hour)})
	}
	gql := collector.NewFixturePRClient(nodes)
	coll, reader, mp := newCollector(t, gql, config.PRFilters{})

	result, err := coll.Collect(context.Background(), "tgt", oneRepo, nil, now)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, err := mp.Meter("test").RegisterCallback(coll.ObservableRegistration(result), coll.ObservableInstruments()...); err != nil {
		t.Fatalf("RegisterCallback: %v", err)
	}

	counts := map[string]int64{}
	for _, dp := range findGaugeInt(t, collectMetrics(t, reader), semconv.MetricGitHubPrCount) {
		v, _ := dp.Attributes.Value(semconv.AttrGitHubPrState)
		counts[v.AsString()] += dp.Value
	}
	if counts["open"] != 1200 || counts["draft"] != 50 {
		t.Errorf("github.pr.count = %v, want open=1200 draft=50", counts)
	}
	if got := gql.RequestCount("CREATED_AT"); got != 13 {
		t.Errorf("open scan pages = %d, want 13", got)
	}
	for _, r := range gql.Requests() {
		if len(r.States) != 1 || r.States[0] != "OPEN" {
			t.Errorf("open scan states = %v, want [OPEN]", r.States)
		}
	}
}

// The event scan stops after the first page that ends before the window
// start, and only events inside the window are counted.
func TestPRCollector_EventScanStopsAtWindowStart(t *testing.T) {
	now := time.Now()
	since := now.Add(-2 * time.Hour)
	var nodes []collector.ExportedPRNode
	// 15 PRs merged inside the window.
	for i := range 15 {
		m := now.Add(-time.Duration(i+1) * time.Minute)
		nodes = append(nodes, collector.ExportedPRNode{Number: i + 1, State: "MERGED", CreatedAt: now.Add(-48 * time.Hour), MergedAt: &m})
	}
	// 200 PRs merged and last updated before the window.
	for i := range 200 {
		m := since.Add(-time.Duration(i+1) * time.Hour)
		nodes = append(nodes, collector.ExportedPRNode{Number: 100 + i, State: "MERGED", CreatedAt: m.Add(-time.Hour), MergedAt: &m})
	}
	gql := collector.NewFixturePRClient(nodes)
	gql.PageSize = 10
	coll, reader, _ := newCollector(t, gql, config.PRFilters{})

	if _, err := coll.Collect(context.Background(), "tgt", oneRepo, sinceAll(oneRepo, since), now); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// Page 1: 10 in-window PRs. Page 2: 5 in-window + 5 older → stop.
	if got := gql.RequestCount("UPDATED_AT"); got != 2 {
		t.Errorf("event scan pages = %d, want 2", got)
	}
	if got := readEventTotals(t, reader); got.merged != 15 || got.closed != 0 || got.created != 0 {
		t.Errorf("events = %+v, want merged=15 closed=0 created=0", got)
	}
}

// A previous window older than the lookback is capped at the lookback.
func TestPRCollector_LookbackCapsWindow(t *testing.T) {
	now := time.Now()
	recent := now.Add(-24 * time.Hour)
	old := now.Add(-35 * 24 * time.Hour)
	nodes := []collector.ExportedPRNode{
		{Number: 1, State: "MERGED", CreatedAt: recent.Add(-time.Hour), MergedAt: &recent},
		{Number: 2, State: "MERGED", CreatedAt: old.Add(-time.Hour), MergedAt: &old},
	}
	coll, reader, _ := newCollector(t, collector.NewFixturePRClient(nodes), config.PRFilters{})

	if _, err := coll.Collect(context.Background(), "tgt", oneRepo, sinceAll(oneRepo, now.Add(-40*24*time.Hour)), now); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := readEventTotals(t, reader); got.merged != 1 {
		t.Errorf("merged = %d, want 1 (35-day-old merge is outside the 30-day lookback)", got.merged)
	}
}

// created, merged and closed count disjoint events in the window.
func TestPRCollector_EventCounters(t *testing.T) {
	now := time.Now()
	since := now.Add(-time.Hour)
	in := now.Add(-30 * time.Minute)
	before := now.Add(-3 * time.Hour)
	approved := []string{"APPROVED"}
	nodes := []collector.ExportedPRNode{
		{Number: 1, State: "OPEN", CreatedAt: in},                                                // created
		{Number: 2, State: "MERGED", CreatedAt: before, MergedAt: &in},                           // merged, automerged
		{Number: 3, State: "MERGED", CreatedAt: in, MergedAt: &in, Reviews: approved},            // created + merged
		{Number: 4, State: "CLOSED", CreatedAt: before, ClosedAt: &in},                           // closed
		{Number: 5, State: "CLOSED", CreatedAt: before, ClosedAt: &before},                       // outside window
		{Number: 6, State: "MERGED", CreatedAt: before, MergedAt: ptrTime(now.Add(time.Minute))}, // after window end
	}
	coll, reader, _ := newCollector(t, collector.NewFixturePRClient(nodes), config.PRFilters{})

	if _, err := coll.Collect(context.Background(), "tgt", oneRepo, sinceAll(oneRepo, since), now); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rm := collectMetrics(t, reader)
	got := eventTotals{
		created:    sumCounter(t, rm, semconv.MetricGitHubPrCreated),
		merged:     sumCounter(t, rm, semconv.MetricGitHubPrMerged),
		closed:     sumCounter(t, rm, semconv.MetricGitHubPrClosed),
		automerged: sumCounter(t, rm, semconv.MetricGitHubPrAutomerged),
	}
	want := eventTotals{created: 2, merged: 2, closed: 1, automerged: 1}
	if got != want {
		t.Errorf("events = %+v, want %+v", got, want)
	}
	if h := findHistogram(t, rm, semconv.MetricGitHubPrCloseDuration); len(h) != 1 || h[0].Count != 3 {
		t.Errorf("close.duration points = %+v, want one point with count 3", h)
	}
	for _, dp := range findGaugeInt(t, rm, semconv.MetricGitHubPrMerged) {
		for _, key := range []string{semconv.AttrExporterTarget, semconv.AttrGitHubOrg, semconv.AttrGitHubRepo} {
			if _, ok := dp.Attributes.Value(attribute.Key(key)); !ok {
				t.Errorf("github.pr.merged point lacks %s", key)
			}
		}
		if dp.Attributes.Len() != 3 {
			t.Errorf("github.pr.merged attributes = %v, want exactly target, org, repo", dp.Attributes.ToSlice())
		}
	}
}

// Every filter type selects PRs for both the open gauges and the event counters.
func TestPRCollector_FiltersSelect(t *testing.T) {
	now := time.Now()
	in := now.Add(-30 * time.Minute)
	before := now.Add(-48 * time.Hour)
	merged := func(n int, author string, bot bool, branch string, labels ...string) collector.ExportedPRNode {
		return collector.ExportedPRNode{Number: n, State: "MERGED", CreatedAt: before, MergedAt: &in,
			Author: author, AuthorIsBot: bot, HeadRefName: branch, Labels: labels}
	}
	open := func(n int, author string, bot bool, branch string, labels ...string) collector.ExportedPRNode {
		return collector.ExportedPRNode{Number: n, State: "OPEN", CreatedAt: before,
			Author: author, AuthorIsBot: bot, HeadRefName: branch, Labels: labels}
	}
	nodes := []collector.ExportedPRNode{
		merged(1, "renovate", true, "renovate/a", "dependencies"),
		merged(2, "alice", false, "feature/x", "bug"),
		merged(3, "renovate-bot", false, "renovate/b"),
		open(11, "renovate", true, "renovate/c", "dependencies"),
		open(12, "alice", false, "feature/y"),
		open(13, "renovate-bot", false, "renovate/d", "dependencies", "wip"),
	}

	tests := []struct {
		name       string
		filters    config.PRFilters
		wantMerged int64
		wantOpen   int64
		wantLabels []string // server-side $labels
	}{
		{"bot author", config.PRFilters{Authors: []string{"renovate[bot]"}}, 1, 1, nil},
		{"self-hosted user author", config.PRFilters{Authors: []string{"renovate-bot"}}, 1, 1, nil},
		{"branch prefix", config.PRFilters{HeadBranchPrefix: "renovate/"}, 2, 2, nil},
		{"include label", config.PRFilters{IncludeLabels: []string{"dependencies"}}, 1, 2, []string{"dependencies"}},
		{"exclude label", config.PRFilters{IncludeLabels: []string{"dependencies"}, ExcludeLabels: []string{"wip"}}, 1, 1, []string{"dependencies"}},
		{"state merged only", config.PRFilters{States: []string{"merged"}}, 3, 0, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gql := collector.NewFixturePRClient(nodes)
			coll, reader, mp := newCollector(t, gql, tc.filters)
			result, err := coll.Collect(context.Background(), "tgt", oneRepo, sinceAll(oneRepo, now.Add(-time.Hour)), now)
			if err != nil {
				t.Fatalf("Collect: %v", err)
			}
			if _, err := mp.Meter("test").RegisterCallback(coll.ObservableRegistration(result), coll.ObservableInstruments()...); err != nil {
				t.Fatalf("RegisterCallback: %v", err)
			}
			rm := collectMetrics(t, reader)
			if got := sumCounter(t, rm, semconv.MetricGitHubPrMerged); got != tc.wantMerged {
				t.Errorf("merged = %d, want %d", got, tc.wantMerged)
			}
			var openCount int64
			for _, dp := range findGaugeInt(t, rm, semconv.MetricGitHubPrCount) {
				if _, hasLabel := dp.Attributes.Value(semconv.AttrGitHubPrLabel); !hasLabel {
					openCount += dp.Value
				}
			}
			if openCount != tc.wantOpen {
				t.Errorf("open count = %d, want %d", openCount, tc.wantOpen)
			}
			for _, r := range gql.Requests() {
				if strings.Join(r.Labels, ",") != strings.Join(tc.wantLabels, ",") {
					t.Errorf("server-side labels = %v, want %v", r.Labels, tc.wantLabels)
				}
			}
		})
	}
}

// On a repo's first cycle no events are recorded, but the counters exist at 0.
func TestPRCollector_FirstCycleNoEvents(t *testing.T) {
	now := time.Now()
	in := now.Add(-time.Minute)
	nodes := []collector.ExportedPRNode{
		{Number: 1, State: "MERGED", CreatedAt: in, MergedAt: &in},
		{Number: 2, State: "CLOSED", CreatedAt: in, ClosedAt: &in},
	}
	gql := collector.NewFixturePRClient(nodes)
	coll, reader, _ := newCollector(t, gql, config.PRFilters{})

	if _, err := coll.Collect(context.Background(), "tgt", oneRepo, nil, now); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := gql.RequestCount("UPDATED_AT"); got != 0 {
		t.Errorf("event scan requests on first cycle = %d, want 0", got)
	}
	rm := collectMetrics(t, reader)
	for _, name := range []string{semconv.MetricGitHubPrCreated, semconv.MetricGitHubPrMerged, semconv.MetricGitHubPrClosed} {
		if counterPoints(t, rm, name) != 1 || sumCounter(t, rm, name) != 0 {
			t.Errorf("%s: want one zero-valued point, got %v", name, findGaugeInt(t, rm, name))
		}
	}
	if got := sumCounter(t, rm, semconv.MetricGitHubPrAutomerged); got != 0 {
		t.Errorf("automerged = %d, want 0", got)
	}
	if h := findHistogram(t, rm, semconv.MetricGitHubPrCloseDuration); len(h) != 0 {
		t.Errorf("close.duration recorded on first cycle: %+v", h)
	}
}

// Consecutive windows share only their boundary: an event at the boundary
// and events seen by both scans are counted once.
func TestPRCollector_NoDoubleCountingAcrossCycles(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	t1 := t0.Add(20 * time.Minute)
	t2 := t1.Add(20 * time.Minute)
	mid := t0.Add(10 * time.Minute)
	nodes := []collector.ExportedPRNode{
		{Number: 1, State: "MERGED", CreatedAt: t0.Add(-time.Hour), MergedAt: &t1}, // exactly at boundary
		{Number: 2, State: "MERGED", CreatedAt: t0.Add(-time.Hour), MergedAt: &mid},
		{Number: 3, State: "CLOSED", CreatedAt: mid, ClosedAt: &mid},
	}
	coll, reader, _ := newCollector(t, collector.NewFixturePRClient(nodes), config.PRFilters{})

	if _, err := coll.Collect(context.Background(), "tgt", oneRepo, sinceAll(oneRepo, t0), t1); err != nil {
		t.Fatalf("Collect cycle 1: %v", err)
	}
	if _, err := coll.Collect(context.Background(), "tgt", oneRepo, sinceAll(oneRepo, t1), t2); err != nil {
		t.Fatalf("Collect cycle 2: %v", err)
	}
	want := eventTotals{created: 1, merged: 2, closed: 1, automerged: 2}
	if got := readEventTotals(t, reader); got != want {
		t.Errorf("events after two cycles = %+v, want %+v", got, want)
	}
}

// A repo whose event scan fails records no event and is reported failed.
func TestPRCollector_FailedEventScanRecordsNothing(t *testing.T) {
	now := time.Now()
	in := now.Add(-time.Minute)
	gql := collector.NewFixturePRClient([]collector.ExportedPRNode{
		{Number: 1, State: "MERGED", CreatedAt: in, MergedAt: &in},
	})
	gql.FailEventScan = true
	coll, reader, _ := newCollector(t, gql, config.PRFilters{})

	result, err := coll.Collect(context.Background(), "tgt", oneRepo, sinceAll(oneRepo, now.Add(-time.Hour)), now)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if f := result.FailedRepos(); len(f) != 1 || f[0] != "org/repo" {
		t.Errorf("failed repos = %v, want [org/repo]", f)
	}
	rm := collectMetrics(t, reader)
	for _, name := range []string{semconv.MetricGitHubPrCreated, semconv.MetricGitHubPrMerged, semconv.MetricGitHubPrClosed, semconv.MetricGitHubPrAutomerged} {
		if n := counterPoints(t, rm, name); n != 0 {
			t.Errorf("%s has %d points after a failed scan, want 0", name, n)
		}
	}
}
