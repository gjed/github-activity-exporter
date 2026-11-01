package collector_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/gjed/renovate-exporter/internal/collector"
	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/discovery"
)

type staticRepoSource struct{}

func (staticRepoSource) Repos(context.Context) ([]discovery.Repo, error) { return nil, nil }

type countingScrapeRecorder struct{ targets []string }

func (c *countingScrapeRecorder) RecordScrapeDuration(_ context.Context, target string, _ time.Time) {
	c.targets = append(c.targets, target)
}

func TestRunner_RecordsScrapeDurationPerCycle(t *testing.T) {
	rec := &countingScrapeRecorder{}
	meter := metric.NewMeterProvider().Meter("test")

	r, err := collector.NewRunner(config.Target{Name: "tgt"}, staticRepoSource{},
		collector.RunnerCollectors{}, meter, nil, collector.WithScrapeRecorder(rec))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	r.RunOnce(context.Background())
	r.RunOnce(context.Background())

	if len(rec.targets) != 2 || rec.targets[0] != "tgt" {
		t.Fatalf("scrape recordings = %v, want 2 for target tgt", rec.targets)
	}
}
