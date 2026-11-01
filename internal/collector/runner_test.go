package collector_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/gjed/renovate-exporter/internal/collector"
	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/discovery"
	"github.com/gjed/renovate-exporter/internal/semconv"
)

type staticRepoSource struct{ repos []discovery.Repo }

func (s staticRepoSource) Repos(context.Context) ([]discovery.Repo, error) { return s.repos, nil }

type fakeSelfMetrics struct{ scrapes []string }

func (f *fakeSelfMetrics) RecordScrapeDuration(_ context.Context, target string, _ time.Time) {
	f.scrapes = append(f.scrapes, target)
}

// flakyPRClient returns nodes, or an error while fail is set.
type flakyPRClient struct {
	fail  bool
	nodes []collector.ExportedPRNode
}

func (f *flakyPRClient) Query(_ context.Context, q interface{}, _ map[string]interface{}) error {
	if f.fail {
		return errors.New("graphql: 502 bad gateway")
	}
	return collector.InjectPRQueryResult(q, f.nodes, false)
}

func TestRunner_RecordsScrapeDurationPerCycle(t *testing.T) {
	self := &fakeSelfMetrics{}
	meter := metric.NewMeterProvider().Meter("test")

	r, err := collector.NewRunner(config.Target{Name: "tgt"}, staticRepoSource{},
		collector.RunnerCollectors{}, meter, nil, collector.WithScrapeRecorder(self))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	r.RunOnce(context.Background())
	r.RunOnce(context.Background())

	if len(self.scrapes) != 2 || self.scrapes[0] != "tgt" {
		t.Fatalf("scrape recordings = %v, want 2 for target tgt", self.scrapes)
	}
}

// A PR merged while its repo's collection is failing must still be counted
// once the repo recovers: a failed repo must not advance lastCollectedAt.
func TestRunner_FailedRepoKeepsEventWindow(t *testing.T) {
	ctx := context.Background()
	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")
	gql := &flakyPRClient{}

	prColl, err := collector.NewPRCollector(gql, config.PRFilters{},
		collector.PRCollectorConfig{MaxPRsPerRepo: 500, LookbackDays: 30}, meter, nil)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}
	repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
	r, err := collector.NewRunner(config.Target{Name: "tgt"}, staticRepoSource{repos: repos},
		collector.RunnerCollectors{PR: prColl}, meter, nil)
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	r.RunOnce(ctx) // cycle 1: no PRs, establishes the window

	time.Sleep(5 * time.Millisecond)
	mergedAt := time.Now()
	time.Sleep(5 * time.Millisecond)

	gql.fail = true
	r.RunOnce(ctx) // cycle 2: repo fails

	gql.fail = false
	gql.nodes = []collector.ExportedPRNode{{
		Number: 1, State: "MERGED", CreatedAt: mergedAt.Add(-time.Hour), MergedAt: &mergedAt,
	}}
	r.RunOnce(ctx) // cycle 3: repo recovers, PR merged before cycle 2 started

	rm := collectMetrics(t, reader)
	var automerged int64
	for _, dp := range findGaugeInt(t, rm, semconv.MetricGitHubPrAutomerged) {
		automerged += dp.Value
	}
	if automerged != 1 {
		t.Errorf("github.pr.automerged = %d, want 1", automerged)
	}
}
