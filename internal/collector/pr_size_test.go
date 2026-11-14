package collector_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/semconv"
)

func findIntHistogram(t *testing.T, rm metricdata.ResourceMetrics, name string) []metricdata.HistogramDataPoint[int64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				h, ok := m.Data.(metricdata.Histogram[int64])
				if !ok {
					t.Fatalf("metric %q is not an int64 Histogram", name)
				}
				return h.DataPoints
			}
		}
	}
	return nil
}

func newAuthorCollector(t *testing.T, gql collector.GraphQLClient, prAuthor bool) (*collector.PRCollector, *metric.ManualReader) {
	t.Helper()
	mp, reader := newTestMeterProvider()
	coll, err := collector.NewPRCollector(gql, config.PRFilters{},
		collector.PRCollectorConfig{LookbackDays: 30, PRAuthor: prAuthor}, mp.Meter("test"), nil)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}
	return coll, reader
}

// sizeNodes: two merged PRs and one closed PR in the window, one merged PR
// before it, one open PR.
func sizeNodes(now time.Time) []collector.ExportedPRNode {
	in := now.Add(-30 * time.Minute)
	before := now.Add(-3 * time.Hour)
	return []collector.ExportedPRNode{
		{Number: 1, State: "MERGED", CreatedAt: before, MergedAt: &in, Additions: 120, Deletions: 30, Author: "alice"},
		{Number: 2, State: "MERGED", CreatedAt: before, MergedAt: &in, Additions: 3, Deletions: 1, Author: "renovate", AuthorIsBot: true},
		{Number: 3, State: "CLOSED", CreatedAt: before, ClosedAt: &in, Additions: 7000, Deletions: 0},
		{Number: 4, State: "MERGED", CreatedAt: before, MergedAt: &before, Additions: 99, Deletions: 99, Author: "alice"},
		{Number: 5, State: "OPEN", CreatedAt: in, Additions: 99, Deletions: 99, Author: "alice"},
	}
}

func histAttrs(dp metricdata.HistogramDataPoint[int64]) string {
	return dp.Attributes.Encoded(attribute.DefaultEncoder())
}

// With the setting off, the size histograms record every merged or closed
// PR of the window once, per repo, and carry no author.
func TestPRCollector_SizeWithoutAuthor(t *testing.T) {
	now := time.Now()
	coll, reader := newAuthorCollector(t, collector.NewFixturePRClient(sizeNodes(now)), false)
	if _, err := coll.Collect(context.Background(), "tgt", oneRepo, sinceAll(oneRepo, now.Add(-time.Hour)), now); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rm := collectMetrics(t, reader)

	for name, wantSum := range map[string]int64{
		semconv.MetricGitHubPrLinesAdded:   120 + 3 + 7000,
		semconv.MetricGitHubPrLinesRemoved: 30 + 1 + 0,
	} {
		dps := findIntHistogram(t, rm, name)
		if len(dps) != 1 {
			t.Fatalf("%s: %d points, want 1", name, len(dps))
		}
		if dps[0].Count != 3 || dps[0].Sum != wantSum {
			t.Errorf("%s: count %d sum %d, want 3 and %d", name, dps[0].Count, dps[0].Sum, wantSum)
		}
		if got := histAttrs(dps[0]); got != "exporter.target=tgt,github.org=org,github.repo=repo" {
			t.Errorf("%s attributes = %s", name, got)
		}
	}
	added := findIntHistogram(t, rm, semconv.MetricGitHubPrLinesAdded)[0]
	wantBounds := []float64{0, 10, 50, 100, 250, 500, 1000, 2500, 5000, 10000}
	if !slices.Equal(added.Bounds, wantBounds) {
		t.Errorf("bounds = %v, want %v", added.Bounds, wantBounds)
	}
	// 3 → (0,10], 120 → (100,250], 7000 → (5000,10000].
	if want := []uint64{0, 1, 0, 0, 1, 0, 0, 0, 0, 1, 0}; !slices.Equal(added.BucketCounts, want) {
		t.Errorf("lines.added buckets = %v, want %v", added.BucketCounts, want)
	}

	cd := findHistogram(t, rm, semconv.MetricGitHubPrCloseDuration)
	if len(cd) != 1 || cd[0].Count != 3 || cd[0].Attributes.Len() != 3 {
		t.Errorf("close.duration = %+v, want one point with count 3 and target, org, repo", cd)
	}
}

// With the setting on, the three histograms carry the GraphQL login
// verbatim, and a PR without author is recorded without the attribute.
func TestPRCollector_SizeWithAuthor(t *testing.T) {
	now := time.Now()
	coll, reader := newAuthorCollector(t, collector.NewFixturePRClient(sizeNodes(now)), true)
	if _, err := coll.Collect(context.Background(), "tgt", oneRepo, sinceAll(oneRepo, now.Add(-time.Hour)), now); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rm := collectMetrics(t, reader)

	base := "exporter.target=tgt,github.org=org,"
	want := map[string]uint64{
		base + "github.pr.author=alice,github.repo=repo":    1,
		base + "github.pr.author=renovate,github.repo=repo": 1,
		base + "github.repo=repo":                           1,
	}
	for _, name := range []string{semconv.MetricGitHubPrLinesAdded, semconv.MetricGitHubPrLinesRemoved} {
		got := map[string]uint64{}
		for _, dp := range findIntHistogram(t, rm, name) {
			got[histAttrs(dp)] = dp.Count
		}
		if len(got) != len(want) {
			t.Errorf("%s points = %v, want %v", name, got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s{%s} count = %d, want %d", name, k, got[k], v)
			}
		}
	}
	got := map[string]uint64{}
	for _, dp := range findHistogram(t, rm, semconv.MetricGitHubPrCloseDuration) {
		got[dp.Attributes.Encoded(attribute.DefaultEncoder())] = dp.Count
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("close.duration{%s} count = %d, want %d", k, got[k], v)
		}
	}
	// The counters never carry the author.
	for _, name := range []string{semconv.MetricGitHubPrMerged, semconv.MetricGitHubPrClosed, semconv.MetricGitHubPrCreated} {
		for _, dp := range findGaugeInt(t, rm, name) {
			if _, ok := dp.Attributes.Value(semconv.AttrGitHubPrAuthor); ok {
				t.Errorf("%s carries github.pr.author", name)
			}
		}
	}
}

// A PR seen by two consecutive cycles is recorded once, and the first cycle
// records no size.
func TestPRCollector_SizeOncePerPR(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	t1 := t0.Add(20 * time.Minute)
	t2 := t1.Add(20 * time.Minute)
	t3 := t2.Add(20 * time.Minute)
	mid := t1.Add(10 * time.Minute)
	nodes := []collector.ExportedPRNode{
		{Number: 1, State: "MERGED", CreatedAt: t0.Add(-time.Hour), MergedAt: &t2, Additions: 5, Deletions: 2, Author: "alice"}, // at boundary
		{Number: 2, State: "CLOSED", CreatedAt: t0, ClosedAt: &mid, Additions: 40, Deletions: 8, Author: "bob"},
	}
	coll, reader := newAuthorCollector(t, collector.NewFixturePRClient(nodes), true)

	if _, err := coll.Collect(context.Background(), "tgt", oneRepo, nil, t1); err != nil {
		t.Fatalf("cycle 1: %v", err)
	}
	if h := findIntHistogram(t, collectMetrics(t, reader), semconv.MetricGitHubPrLinesAdded); len(h) != 0 {
		t.Errorf("lines.added recorded on first cycle: %+v", h)
	}
	for _, w := range [][2]time.Time{{t1, t2}, {t2, t3}} {
		if _, err := coll.Collect(context.Background(), "tgt", oneRepo, sinceAll(oneRepo, w[0]), w[1]); err != nil {
			t.Fatalf("Collect: %v", err)
		}
	}
	var count uint64
	var sum int64
	for _, dp := range findIntHistogram(t, collectMetrics(t, reader), semconv.MetricGitHubPrLinesAdded) {
		count += dp.Count
		sum += dp.Sum
	}
	if count != 2 || sum != 45 {
		t.Errorf("lines.added after three cycles: count %d sum %d, want 2 and 45", count, sum)
	}
}
