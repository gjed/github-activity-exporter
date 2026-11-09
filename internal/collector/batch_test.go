package collector_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/semconv"
)

// batchRepos returns n repos of org "org", including names that are not
// valid GraphQL aliases.
func batchRepos(n int) []discovery.Repo {
	repos := make([]discovery.Repo, n)
	for i := range repos {
		name := "repo-" + strconv.Itoa(i)
		switch i {
		case 1:
			name = ".github"
		case 2:
			name = "my-repo.v2"
		}
		repos[i] = discovery.Repo{Owner: "org", Name: name, FullName: "org/" + name}
	}
	return repos
}

// batchFixture serves every repo the same issues: three labels and a
// Renovate Dependency Dashboard.
func batchFixture() *collector.FixtureIssueClient {
	now := time.Now()
	return &collector.FixtureIssueClient{
		Issues: []collector.ExportedIssueNode{
			fixtureIssue("OPEN", "a", now.Add(-48*time.Hour), "bug", "ui"),
			fixtureIssue("OPEN", "b", now.Add(-time.Hour), "bug"),
			fixtureIssue("CLOSED", "c", now.Add(-96*time.Hour), "docs"),
			dashboardIssue("Dependency Dashboard", typicalDashboard, "renovate[bot]"),
		},
	}
}

// batchRun is what one Collect of the issue and dashboard collectors emitted.
type batchRun struct {
	// series lists every exported data point as "<metric>{attrs}" or
	// "<metric>{attrs}=<value>"; github.issue.age values are left out, as
	// they move with the clock.
	series []string
	failed []string
}

// collectBatched runs the issue and dashboard collectors once over repos
// with the given batch size.
func collectBatched(t *testing.T, issues, dash *collector.FixtureIssueClient, repos []discovery.Repo, size int) batchRun {
	t.Helper()
	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	iss, err := collector.NewIssueCollector(issues, config.IssueFilters{}, meter, nil, collector.WithGraphQLBatchSize(size))
	if err != nil {
		t.Fatalf("NewIssueCollector: %v", err)
	}
	dc, err := collector.NewDashboardCollector(dash, "renovate[bot]", meter, nil, collector.WithGraphQLBatchSize(size))
	if err != nil {
		t.Fatalf("NewDashboardCollector: %v", err)
	}
	ir, _ := iss.Collect(context.Background(), "tgt", repos)
	dr, _ := dc.Collect(context.Background(), "tgt", repos)
	if _, err := meter.RegisterCallback(iss.ObservableRegistration(ir), iss.ObservableInstruments()...); err != nil {
		t.Fatalf("RegisterCallback: %v", err)
	}
	if _, err := meter.RegisterCallback(dc.ObservableRegistration(dr), dc.ObservableInstruments()...); err != nil {
		t.Fatalf("RegisterCallback: %v", err)
	}

	run := batchRun{failed: append(ir.FailedRepos(), dr.FailedRepos()...)}
	rm := collectMetrics(t, reader)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, dp := range d.DataPoints {
					run.series = append(run.series, fmt.Sprintf("%s{%s}=%d", m.Name, attrString(dp.Attributes), dp.Value))
				}
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					run.series = append(run.series, fmt.Sprintf("%s{%s}=%d", m.Name, attrString(dp.Attributes), dp.Value))
				}
			case metricdata.Gauge[float64]:
				for _, dp := range d.DataPoints {
					run.series = append(run.series, fmt.Sprintf("%s{%s}", m.Name, attrString(dp.Attributes)))
				}
			default:
				t.Fatalf("unexpected data type %T for %s", m.Data, m.Name)
			}
		}
	}
	sort.Strings(run.series)
	return run
}

func attrString(s attribute.Set) string {
	return s.Encoded(attribute.DefaultEncoder())
}

func TestBatch_SplitsReposAndMatchesPerRepo(t *testing.T) {
	repos := batchRepos(45)
	perRepo := collectBatched(t, batchFixture(), batchFixture(), repos, 1)

	issues, dash := batchFixture(), batchFixture()
	batched := collectBatched(t, issues, dash, repos, 20)

	if got := issues.Batches(); fmt.Sprint(got) != "[20 20 5]" {
		t.Errorf("issue batches = %v, want [20 20 5]", got)
	}
	if got := dash.Batches(); fmt.Sprint(got) != "[20 20 5]" {
		t.Errorf("dashboard batches = %v, want [20 20 5]", got)
	}
	if got := len(issues.Requests()) + len(dash.Requests()); got != 6 {
		t.Errorf("requests = %d, want 6 (one per batch)", got)
	}
	if len(batched.failed) != 0 {
		t.Errorf("failed = %v, want none", batched.failed)
	}
	if !slices.Equal(batched.series, perRepo.series) {
		t.Errorf("batched series differ from per-repo series:\nbatched: %v\nper-repo: %v", batched.series, perRepo.series)
	}
	for _, name := range []string{".github", "my-repo.v2"} {
		want := fmt.Sprintf("%s=%s", semconv.AttrGitHubRepo, name)
		if !slices.ContainsFunc(batched.series, func(s string) bool { return containsAll(s, semconv.MetricRenovateDashboardQueue, want) }) ||
			!slices.ContainsFunc(batched.series, func(s string) bool { return containsAll(s, semconv.MetricGitHubIssueCount, want) }) {
			t.Errorf("no issue or dashboard series for repo %q", name)
		}
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// A repo GitHub answers NOT_FOUND for fails alone, without a per-repo retry.
func TestBatch_AliasErrorFailsOnlyThatRepo(t *testing.T) {
	repos := batchRepos(5)
	issues, dash := batchFixture(), batchFixture()
	issues.AliasErrors = map[string]string{"repo-3": "NOT_FOUND"}
	dash.AliasErrors = map[string]string{"repo-3": "FORBIDDEN"}
	run := collectBatched(t, issues, dash, repos, 20)

	if want := []string{"org/repo-3", "org/repo-3"}; !slices.Equal(run.failed, want) {
		t.Errorf("failed = %v, want %v", run.failed, want)
	}
	if got := len(issues.Requests()) + len(dash.Requests()); got != 2 {
		t.Errorf("requests = %d, want 2 (the batches, no retry)", got)
	}
	ok := collectBatched(t, batchFixture(), batchFixture(), slices.Delete(slices.Clone(repos), 3, 4), 20)
	if !slices.Equal(run.series, ok.series) {
		t.Errorf("series = %v, want the other repos' series %v", run.series, ok.series)
	}
}

// A resource limit on a field under a repo's alias leaves partial data: the
// repo is read again with the per-repo query, and nothing fails.
func TestBatch_ResourceLimitFallsBackPerRepo(t *testing.T) {
	repos := batchRepos(5)
	want := collectBatched(t, batchFixture(), batchFixture(), repos, 20)

	issues, dash := batchFixture(), batchFixture()
	issues.AliasErrors = map[string]string{"repo-3": "RESOURCE_LIMITS_EXCEEDED"}
	dash.AliasErrors = map[string]string{"repo-3": "RESOURCE_LIMITS_EXCEEDED"}
	run := collectBatched(t, issues, dash, repos, 20)

	if len(run.failed) != 0 {
		t.Errorf("failed = %v, want none", run.failed)
	}
	if got := len(issues.Requests()); got != 2 {
		t.Errorf("issue requests = %d, want 2 (batch + one per-repo)", got)
	}
	if got := len(dash.Requests()); got != 2 {
		t.Errorf("dashboard requests = %d, want 2 (batch + one per-repo)", got)
	}
	if !slices.Equal(run.series, want.series) {
		t.Errorf("series = %v, want %v", run.series, want.series)
	}
}

// A batch request that fails as a whole is retried per repo.
func TestBatch_WholeBatchFailureRetriesPerRepo(t *testing.T) {
	repos := batchRepos(5)
	want := collectBatched(t, batchFixture(), batchFixture(), repos, 20)

	issues, dash := batchFixture(), batchFixture()
	issues.RawErr = errors.New("graphql: non-200 OK status code: 502 Bad Gateway")
	dash.RawErr = issues.RawErr
	run := collectBatched(t, issues, dash, repos, 20)

	if len(run.failed) != 0 {
		t.Errorf("failed = %v, want none", run.failed)
	}
	if got := len(issues.Requests()); got != 1+len(repos) {
		t.Errorf("issue requests = %d, want %d (batch + one per repo)", got, 1+len(repos))
	}
	if !slices.Equal(run.series, want.series) {
		t.Errorf("series = %v, want %v", run.series, want.series)
	}
}

// A repo with more label pages, or more dashboard candidates, continues from
// the batch's cursor: its first page is not read twice.
func TestBatch_NextPageContinuesPerRepo(t *testing.T) {
	repos := batchRepos(3)
	want := collectBatched(t, batchFixture(), batchFixture(), repos, 1)

	issues, dash := batchFixture(), batchFixture()
	issues.PageSize, dash.PageSize = 2, 2
	// Two other bot issues come before the dashboard on the first page.
	other := dashboardIssue("Action Required: Fix Renovate Configuration", "", "renovate[bot]")
	dash.Issues = append([]collector.ExportedIssueNode{other, other}, dash.Issues...)
	run := collectBatched(t, issues, dash, repos, 20)

	if len(run.failed) != 0 {
		t.Errorf("failed = %v, want none", run.failed)
	}
	// 3 labels at 2 per page: the batch reads page one of each repo, then
	// each repo reads page two from cursor "2".
	var cursors []string
	for _, r := range issues.Requests() {
		cursors = append(cursors, r.Cursor)
	}
	if fmt.Sprint(cursors) != "[ 2 2 2]" {
		t.Errorf("issue request cursors = %q, want the batch then 2, 2, 2", cursors)
	}
	cursors = nil
	for _, r := range dash.Requests() {
		cursors = append(cursors, r.Cursor)
	}
	if fmt.Sprint(cursors) != "[ 2 2 2]" {
		t.Errorf("dashboard request cursors = %q, want the batch then 2, 2, 2", cursors)
	}
	if !slices.Equal(run.series, want.series) {
		t.Errorf("series = %v, want %v", run.series, want.series)
	}
}

// Batches of one collector run concurrently without sharing state.
func TestBatch_ConcurrentCollect(t *testing.T) {
	mp, _ := newTestMeterProvider()
	meter := mp.Meter("test")
	fixture := batchFixture()
	iss, err := collector.NewIssueCollector(fixture, config.IssueFilters{}, meter, nil, collector.WithGraphQLBatchSize(3))
	if err != nil {
		t.Fatalf("NewIssueCollector: %v", err)
	}
	dc, err := collector.NewDashboardCollector(fixture, "renovate[bot]", meter, nil, collector.WithGraphQLBatchSize(3))
	if err != nil {
		t.Fatalf("NewDashboardCollector: %v", err)
	}

	repos := batchRepos(12)
	var wg sync.WaitGroup
	for _, part := range [][]discovery.Repo{repos[:6], repos[6:]} {
		wg.Go(func() {
			ir, _ := iss.Collect(context.Background(), "tgt", part)
			dr, _ := dc.Collect(context.Background(), "tgt", part)
			if len(ir.FailedRepos())+len(dr.FailedRepos()) != 0 {
				t.Errorf("failed = %v %v", ir.FailedRepos(), dr.FailedRepos())
			}
		})
	}
	wg.Wait()
	if got := len(fixture.Batches()); got != 8 {
		t.Errorf("batches = %d, want 8", got)
	}
}
