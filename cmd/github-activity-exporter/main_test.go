package main

import (
	"context"
	"testing"

	"github.com/google/go-github/v93/github"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
	"github.com/gjed/github-activity-exporter/internal/telemetry"
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

// dashboardIssueClient returns one Renovate Dependency Dashboard issue.
type dashboardIssueClient struct{}

func (dashboardIssueClient) ListByRepo(context.Context, string, string, *github.IssueListByRepoOptions) ([]*github.Issue, *github.Response, error) {
	title, body, login, state := "Dependency Dashboard", "## Open\n- [ ] dep\n", "renovate[bot]", "open"
	return []*github.Issue{{Title: &title, Body: &body, State: &state, User: &github.User{Login: &login}}}, &github.Response{}, nil
}

// TestTargetsShareOneMeter runs two targets on one meter, as main does, and
// checks that both report their series under the exporter's single scope.
func TestTargetsShareOneMeter(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).
		Meter(telemetry.ScopeName, metric.WithInstrumentationVersion("1.2.3"))

	for _, name := range []string{"a", "b"} {
		target := config.Target{Name: name, RenovateDashboard: config.RenovateDashboard{Enabled: true}}
		dash, err := newDashboardCollector(target, dashboardIssueClient{}, meter, nil)
		if err != nil {
			t.Fatalf("target %s: newDashboardCollector: %v", name, err)
		}
		r, err := collector.NewRunner(target, oneRepoSource{}, collector.RunnerCollectors{Dashboard: dash}, meter, nil)
		if err != nil {
			t.Fatalf("target %s: NewRunner: %v", name, err)
		}
		r.RunOnce(context.Background())
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(rm.ScopeMetrics) != 1 {
		t.Fatalf("got %d scopes, want 1", len(rm.ScopeMetrics))
	}
	sm := rm.ScopeMetrics[0]
	if sm.Scope.Name != telemetry.ScopeName || sm.Scope.Version != "1.2.3" {
		t.Errorf("scope = %+v", sm.Scope)
	}
	targets := map[string]bool{}
	for _, m := range sm.Metrics {
		if g, ok := m.Data.(metricdata.Gauge[int64]); ok {
			for _, dp := range g.DataPoints {
				v, _ := dp.Attributes.Value(attribute.Key(sc.AttrExporterTarget))
				targets[v.AsString()] = true
			}
		}
	}
	if !targets["a"] || !targets["b"] {
		t.Errorf("series by target = %v, want a and b", targets)
	}
}
