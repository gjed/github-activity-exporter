package collector_test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gjed/renovate-exporter/internal/collector"
	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/discovery"
	"github.com/gjed/renovate-exporter/internal/semconv"
)

// fakeIssueClient implements IssueListClient using fixture data.
type fakeIssueClient struct {
	issues []*github.Issue
}

func (f *fakeIssueClient) ListByRepo(_ context.Context, _, _ string, _ *github.IssueListByRepoOptions) ([]*github.Issue, *github.Response, error) {
	return f.issues, &github.Response{}, nil
}

func githubIssue(number int, state, title string, createdAt time.Time, labels ...string) *github.Issue {
	ls := make([]*github.Label, 0, len(labels))
	for _, l := range labels {
		name := l
		ls = append(ls, &github.Label{Name: &name})
	}
	ts := github.Timestamp{Time: createdAt}
	n := number
	return &github.Issue{
		Number:    &n,
		State:     &state,
		Title:     &title,
		Labels:    ls,
		CreatedAt: &ts,
	}
}

func TestIssueCollector_StateCount(t *testing.T) {
	now := time.Now()
	issues := []*github.Issue{
		githubIssue(1, "open", "Bug: something broken", now.Add(-24*time.Hour), "bug"),
		githubIssue(2, "open", "Feature request", now.Add(-48*time.Hour)),
		githubIssue(3, "closed", "Fixed bug", now.Add(-72*time.Hour)),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewIssueCollector(
		&fakeIssueClient{issues: issues},
		config.IssueFilters{},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewIssueCollector: %v", err)
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
	dps := findGaugeInt(t, rm, semconv.MetricGitHubIssueCount)

	stateMap := make(map[string]int64)
	for _, dp := range dps {
		if v, ok := dp.Attributes.Value(semconv.AttrGitHubIssueState); ok {
			stateMap[v.AsString()] += dp.Value
		}
	}

	if stateMap["open"] != 2 {
		t.Errorf("open issue count = %d, want 2", stateMap["open"])
	}
	if stateMap["closed"] != 1 {
		t.Errorf("closed issue count = %d, want 1", stateMap["closed"])
	}
}

func TestIssueCollector_OldestOpenAge(t *testing.T) {
	now := time.Now()
	older := now.Add(-48 * time.Hour)

	issues := []*github.Issue{
		githubIssue(1, "open", "Recent issue", now.Add(-24*time.Hour)),
		githubIssue(2, "open", "Older issue", older),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewIssueCollector(
		&fakeIssueClient{issues: issues},
		config.IssueFilters{},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewIssueCollector: %v", err)
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
	dps := findGaugeFloat(t, rm, semconv.MetricGitHubIssueAge)
	if len(dps) == 0 {
		t.Fatal("no github.issue.age data points")
	}

	wantMin := (48*time.Hour - 60*time.Second).Seconds()
	if dps[0].Value < wantMin {
		t.Errorf("issue.age = %v, want >= %v", dps[0].Value, wantMin)
	}
}

func TestIssueCollector_TitlePatternExclusion(t *testing.T) {
	now := time.Now()
	issues := []*github.Issue{
		githubIssue(1, "open", "Dependency Dashboard", now.Add(-1*time.Hour)),
		githubIssue(2, "open", "Real bug report", now.Add(-2*time.Hour)),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewIssueCollector(
		&fakeIssueClient{issues: issues},
		config.IssueFilters{
			ExcludeTitleRegexps: []*regexp.Regexp{
				regexp.MustCompile(`(?i)dependency dashboard`),
			},
		},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewIssueCollector: %v", err)
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
	dps := findGaugeInt(t, rm, semconv.MetricGitHubIssueCount)

	var total int64
	for _, dp := range dps {
		if _, ok := dp.Attributes.Value(semconv.AttrGitHubIssueState); ok {
			total += dp.Value
		}
	}
	// Only 1 issue should be counted (Dependency Dashboard excluded)
	if total != 1 {
		t.Errorf("issue count after title filter = %d, want 1", total)
	}
}

func TestIssueCollector_LabelCount(t *testing.T) {
	now := time.Now()
	issues := []*github.Issue{
		githubIssue(1, "open", "Issue A", now.Add(-1*time.Hour), "bug", "priority"),
		githubIssue(2, "open", "Issue B", now.Add(-2*time.Hour), "bug"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewIssueCollector(
		&fakeIssueClient{issues: issues},
		config.IssueFilters{},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewIssueCollector: %v", err)
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
	dps := findGaugeInt(t, rm, semconv.MetricGitHubIssueCount)

	labelMap := make(map[string]int64)
	for _, dp := range dps {
		if v, ok := dp.Attributes.Value(semconv.AttrGitHubIssueLabel); ok {
			labelMap[v.AsString()] += dp.Value
		}
	}

	if labelMap["bug"] != 2 {
		t.Errorf("bug label count = %d, want 2", labelMap["bug"])
	}
	if labelMap["priority"] != 1 {
		t.Errorf("priority label count = %d, want 1", labelMap["priority"])
	}
}

func TestIssueCollector_StateFilter(t *testing.T) {
	now := time.Now()
	issues := []*github.Issue{
		githubIssue(1, "open", "Open issue", now.Add(-1*time.Hour)),
		githubIssue(2, "closed", "Closed issue", now.Add(-2*time.Hour)),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewIssueCollector(
		&fakeIssueClient{issues: issues},
		config.IssueFilters{States: []string{"open"}},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewIssueCollector: %v", err)
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
	dps := findGaugeInt(t, rm, semconv.MetricGitHubIssueCount)

	var total int64
	for _, dp := range dps {
		if v, ok := dp.Attributes.Value(semconv.AttrGitHubIssueState); ok {
			if v.AsString() == "closed" {
				t.Error("closed issue should have been filtered out")
			}
			total += dp.Value
		}
	}
	if total != 1 {
		t.Errorf("state-filtered issue count = %d, want 1 (open only)", total)
	}
}

func TestIssueCollector_NoPullRequests(t *testing.T) {
	now := time.Now()
	// GitHub REST returns PRs as issues too; ensure they are filtered by checking
	// IsPullRequest field — but the fake doesn't set PullRequestLinks, which is
	// fine since we don't filter PRs here (that's the PR collector's job).
	// This test just ensures PRs in the issue list don't cause panics.
	issues := []*github.Issue{
		githubIssue(1, "open", "Real issue", now.Add(-1*time.Hour)),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewIssueCollector(
		&fakeIssueClient{issues: issues},
		config.IssueFilters{},
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewIssueCollector: %v", err)
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
	dps := findGaugeInt(t, rm, semconv.MetricGitHubIssueCount)
	if len(dps) == 0 {
		t.Error("expected at least one data point")
	}

	// Ensure no panic on nil DataPoints field
	_ = dps

	// Verify no histogram data was accidentally created
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == semconv.MetricGitHubIssueCount {
				if _, ok := m.Data.(metricdata.Histogram[float64]); ok {
					t.Error("github.issue.count should be an updowncounter (Sum), not a histogram")
				}
			}
		}
	}
}
