package main

import (
	"context"
	"testing"

	"github.com/google/go-github/v93/github"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
)

// countingIssueClient counts dashboard issue requests.
type countingIssueClient struct{ calls int }

func (c *countingIssueClient) ListByRepo(context.Context, string, string, *github.IssueListByRepoOptions) ([]*github.Issue, *github.Response, error) {
	c.calls++
	return nil, &github.Response{}, nil
}

type oneRepoSource struct{}

func (oneRepoSource) Repos(context.Context) ([]discovery.Repo, error) {
	return []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}, nil
}

func TestDashboardCollectorRunsOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		meter := sdkmetric.NewMeterProvider().Meter("test")
		issues := &countingIssueClient{}
		target := config.Target{Name: "tgt", RenovateDashboard: config.RenovateDashboard{Enabled: enabled}}

		dash, err := newDashboardCollector(target, issues, meter, nil)
		if err != nil {
			t.Fatalf("enabled=%v: newDashboardCollector: %v", enabled, err)
		}
		if (dash != nil) != enabled {
			t.Fatalf("enabled=%v: collector built = %v", enabled, dash != nil)
		}

		r, err := collector.NewRunner(target, oneRepoSource{}, collector.RunnerCollectors{Dashboard: dash}, meter, nil)
		if err != nil {
			t.Fatalf("enabled=%v: NewRunner: %v", enabled, err)
		}
		r.RunOnce(context.Background())

		if got := issues.calls > 0; got != enabled {
			t.Errorf("enabled=%v: dashboard issues requested = %v (%d calls)", enabled, got, issues.calls)
		}
	}
}
