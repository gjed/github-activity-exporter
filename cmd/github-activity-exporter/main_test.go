package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/exporter"
	"github.com/gjed/github-activity-exporter/internal/health"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
	"github.com/gjed/github-activity-exporter/internal/telemetry"
)

type oneRepoSource struct{}

func (oneRepoSource) Repos(context.Context) ([]discovery.Repo, error) {
	return []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}, nil
}

func TestDashboardCollectorRunsOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		meter := sdkmetric.NewMeterProvider().Meter("test")
		issues := &collector.FixtureIssueClient{}
		target := config.Target{Name: "tgt", RenovateDashboard: config.RenovateDashboard{Enabled: enabled}}

		dash, err := newDashboardCollector(target, issues, meter, nil, nil)
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

		if got := len(issues.Requests()) > 0; got != enabled {
			t.Errorf("enabled=%v: dashboard issues requested = %v (%d calls)", enabled, got, len(issues.Requests()))
		}
	}
}

// dashboardIssueClient serves one Renovate Dependency Dashboard issue.
func dashboardIssueClient() *collector.FixtureIssueClient {
	return &collector.FixtureIssueClient{Issues: []collector.ExportedIssueNode{{
		State: "OPEN", Title: "Dependency Dashboard", Body: "## Open\n- [ ] dep\n", Author: "renovate", AuthorIsBot: true,
	}}}
}

// TestTargetsShareOneMeter runs two targets on one meter, as main does, and
// checks that both report their series under the exporter's single scope.
func TestTargetsShareOneMeter(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).
		Meter(telemetry.ScopeName, metric.WithInstrumentationVersion("1.2.3"))

	for _, name := range []string{"a", "b"} {
		target := config.Target{Name: name, RenovateDashboard: config.RenovateDashboard{Enabled: true}}
		dash, err := newDashboardCollector(target, dashboardIssueClient(), meter, nil, nil)
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

func TestPermissionCheckOwners(t *testing.T) {
	orgs := config.Target{Orgs: []config.OrgConfig{{Org: "a"}, {Org: "b"}}, RenovateDashboard: config.RenovateDashboard{Enabled: true}}
	if got := permissionCheck(orgs); !slices.Equal(got.Owners, []string{"a", "b"}) || !got.Dashboard {
		t.Errorf("orgs target: %+v", got)
	}
	repos := config.Target{Orgs: []config.OrgConfig{{Org: "ignored"}}, Repos: []string{"x/1", "y/2", "x/3"}}
	if got := permissionCheck(repos); !slices.Equal(got.Owners, []string{"x", "y"}) || got.Dashboard {
		t.Errorf("repos target: %+v", got)
	}
}

// A target whose token lacks a permission fails to build and never becomes
// ready.
func TestBuildTargetRunnerFailsOnMissingPermission(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-OAuth-Scopes", "read:org")
		_, _ = w.Write([]byte(`{"login":"me"}`))
	}))
	defer srv.Close()

	meter := sdkmetric.NewMeterProvider().Meter("test")
	self, err := exporter.NewSelfMetrics(meter)
	if err != nil {
		t.Fatalf("NewSelfMetrics: %v", err)
	}
	tracker := health.NewTracker(time.Hour, 3)
	tracker.Register("tgt")
	target := config.Target{Name: "tgt", BaseURL: srv.URL, Auth: config.Auth{Token: "ghp_x"}, Orgs: []config.OrgConfig{{Org: "acme"}}}

	_, err = buildRunners(context.Background(), &config.Config{Targets: []config.Target{target}},
		slog.New(slog.DiscardHandler), meter, nil, self, tracker)
	if err == nil || !strings.Contains(err.Error(), `target "tgt"`) || !strings.Contains(err.Error(), "missing OAuth scope: repo") {
		t.Fatalf("buildRunners error = %v, want the target and the missing repo scope", err)
	}

	rec := httptest.NewRecorder()
	tracker.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "credential check pending") {
		t.Errorf("/readyz = %d %q, want 503 credential check pending", rec.Code, rec.Body.String())
	}
}
