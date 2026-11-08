package collector_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"testing"
	"time"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/semconv"
)

var issueRepo = discovery.Repo{Owner: "org", Name: "repo", FullName: "org/repo"}

func fixtureIssue(state, title string, createdAt time.Time, labels ...string) collector.ExportedIssueNode {
	return collector.ExportedIssueNode{State: state, Title: title, CreatedAt: createdAt, Labels: labels}
}

// issueSnapshot is what the issue collector emitted for one repo.
type issueSnapshot struct {
	// counts maps "state=<s>" and "label=<l>" to github.issue.count.
	counts map[string]int64
	// age is github.issue.age, -1 when not emitted.
	age float64
	// failed lists the repos whose collection failed.
	failed []string
}

// collectIssues runs the issue collector once against gql and returns its output.
func collectIssues(t *testing.T, gql collector.GraphQLClient, filters config.IssueFilters) issueSnapshot {
	t.Helper()
	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewIssueCollector(gql, filters, meter, nil)
	if err != nil {
		t.Fatalf("NewIssueCollector: %v", err)
	}
	result, err := coll.Collect(context.Background(), "tgt", []discovery.Repo{issueRepo})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, err := meter.RegisterCallback(coll.ObservableRegistration(result), coll.ObservableInstruments()...); err != nil {
		t.Fatalf("RegisterCallback: %v", err)
	}

	rm := collectMetrics(t, reader)
	snap := issueSnapshot{counts: map[string]int64{}, age: -1, failed: result.FailedRepos()}
	for _, dp := range findGaugeInt(t, rm, semconv.MetricGitHubIssueCount) {
		if v, ok := dp.Attributes.Value(semconv.AttrGitHubIssueState); ok {
			snap.counts["state="+v.AsString()] = dp.Value
		}
		if v, ok := dp.Attributes.Value(semconv.AttrGitHubIssueLabel); ok {
			snap.counts["label="+v.AsString()] = dp.Value
		}
	}
	if dps := findGaugeFloat(t, rm, semconv.MetricGitHubIssueAge); len(dps) > 0 {
		snap.age = dps[0].Value
	}
	return snap
}

// requestKinds returns the Query field of every request the fixture received.
func requestKinds(f *collector.FixtureIssueClient) []string {
	var kinds []string
	for _, r := range f.Requests() {
		kinds = append(kinds, r.Query)
	}
	return kinds
}

// assertAge checks that age is the age of createdAt, give or take a minute.
func assertAge(t *testing.T, age float64, createdAt time.Time) {
	t.Helper()
	want := time.Since(createdAt).Seconds()
	if math.Abs(age-want) > 60 {
		t.Errorf("github.issue.age = %v, want about %v", age, want)
	}
}

func TestIssueCollector_ManyPRsFewIssues(t *testing.T) {
	now := time.Now()
	prs := make([]collector.ExportedPRNode, 2000)
	for i := range prs {
		prs[i] = collector.ExportedPRNode{Number: i + 1, State: "MERGED", CreatedAt: now.Add(-time.Duration(i) * time.Hour), Author: "renovate", AuthorIsBot: true}
	}
	oldest := now.Add(-72 * time.Hour)
	issues := []collector.ExportedIssueNode{
		fixtureIssue("OPEN", "Bug: something broken", now.Add(-24*time.Hour), "bug"),
		fixtureIssue("OPEN", "Dependency Dashboard", oldest),
		fixtureIssue("CLOSED", "Fixed bug", now.Add(-96*time.Hour), "bug", "wontfix"),
	}

	t.Run("counted", func(t *testing.T) {
		gql := &collector.FixtureIssueClient{Issues: issues, PullRequests: prs}
		snap := collectIssues(t, gql, config.IssueFilters{})

		if got, want := requestKinds(gql), []string{"issue-count"}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("requests = %v, want %v", got, want)
		}
		want := map[string]int64{"state=open": 2, "state=closed": 1, "label=bug": 2, "label=wontfix": 1}
		if !maps.Equal(snap.counts, want) {
			t.Errorf("counts = %v, want %v", snap.counts, want)
		}
		assertAge(t, snap.age, oldest)
	})

	t.Run("listed for title patterns", func(t *testing.T) {
		gql := &collector.FixtureIssueClient{Issues: issues, PullRequests: prs}
		re := regexp.MustCompile(`(?i)dependency dashboard`)
		snap := collectIssues(t, gql, config.IssueFilters{ExcludeTitleRegexps: []*regexp.Regexp{re}})

		if got, want := requestKinds(gql), []string{"issue-list"}; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("requests = %v, want %v", got, want)
		}
		want := map[string]int64{"state=open": 1, "state=closed": 1, "label=bug": 2, "label=wontfix": 1}
		if !maps.Equal(snap.counts, want) {
			t.Errorf("counts = %v, want %v", snap.counts, want)
		}
		assertAge(t, snap.age, now.Add(-24*time.Hour))
	})
}

// TestIssueCollector_CountsMatchListing checks that the totalCount path emits
// exactly what counting the listed issues emits, for every state filter.
func TestIssueCollector_CountsMatchListing(t *testing.T) {
	now := time.Now()
	issues := []collector.ExportedIssueNode{
		fixtureIssue("OPEN", "a", now.Add(-1*time.Hour), "bug", "p1"),
		fixtureIssue("OPEN", "b", now.Add(-5*time.Hour), "bug"),
		fixtureIssue("OPEN", "c", now.Add(-3*time.Hour)),
		fixtureIssue("CLOSED", "d", now.Add(-9*time.Hour), "bug", "closed-only"),
		fixtureIssue("CLOSED", "e", now.Add(-7*time.Hour), "p1"),
	}
	// A pattern that matches no title forces the listing path without
	// excluding anything.
	noMatch := []*regexp.Regexp{regexp.MustCompile(`^never$`)}

	tests := []struct {
		name   string
		states []string
		want   map[string]int64
		age    bool
	}{
		{"all states", nil, map[string]int64{"state=open": 3, "state=closed": 2, "label=bug": 3, "label=p1": 2, "label=closed-only": 1}, true},
		{"open", []string{"open"}, map[string]int64{"state=open": 3, "label=bug": 2, "label=p1": 1}, true},
		{"closed", []string{"closed"}, map[string]int64{"state=closed": 2, "label=bug": 1, "label=p1": 1, "label=closed-only": 1}, false},
		{"upper case", []string{"OPEN", "Closed"}, map[string]int64{"state=open": 3, "state=closed": 2, "label=bug": 3, "label=p1": 2, "label=closed-only": 1}, true},
		{"no valid state", []string{"draft"}, map[string]int64{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, path := range []struct {
				name    string
				filters config.IssueFilters
			}{
				{"counted", config.IssueFilters{States: tt.states}},
				{"listed", config.IssueFilters{States: tt.states, ExcludeTitleRegexps: noMatch}},
			} {
				gql := &collector.FixtureIssueClient{Issues: issues, ExtraLabels: []string{"unused"}, PageSize: 2}
				snap := collectIssues(t, gql, path.filters)
				if !maps.Equal(snap.counts, tt.want) {
					t.Errorf("%s: counts = %v, want %v", path.name, snap.counts, tt.want)
				}
				if tt.age {
					assertAge(t, snap.age, now.Add(-5*time.Hour))
				} else if snap.age != -1 {
					t.Errorf("%s: github.issue.age = %v, want none", path.name, snap.age)
				}
				if tt.name == "no valid state" && len(gql.Requests()) != 0 {
					t.Errorf("%s: requests = %v, want none", path.name, gql.Requests())
				}
			}
		})
	}
}

func TestIssueCollector_LabelPages(t *testing.T) {
	now := time.Now()
	gql := &collector.FixtureIssueClient{
		Issues: []collector.ExportedIssueNode{
			fixtureIssue("OPEN", "a", now, "l1", "l2", "l3", "l4", "l5"),
		},
		PageSize: 2,
	}
	snap := collectIssues(t, gql, config.IssueFilters{})

	if got := len(gql.Requests()); got != 3 {
		t.Errorf("requests = %d, want 3 label pages", got)
	}
	want := map[string]int64{"state=open": 1, "label=l1": 1, "label=l2": 1, "label=l3": 1, "label=l4": 1, "label=l5": 1}
	if !maps.Equal(snap.counts, want) {
		t.Errorf("counts = %v, want %v", snap.counts, want)
	}
}

func TestIssueCollector_TitlePatternExclusion(t *testing.T) {
	now := time.Now()
	gql := &collector.FixtureIssueClient{
		Issues: []collector.ExportedIssueNode{
			fixtureIssue("OPEN", "Dependency Dashboard", now.Add(-48*time.Hour), "renovate"),
			fixtureIssue("OPEN", "Real bug report", now.Add(-24*time.Hour), "bug"),
			fixtureIssue("OPEN", "Another bug", now.Add(-2*time.Hour), "bug"),
			fixtureIssue("CLOSED", "Renovate bot report", now.Add(-72*time.Hour), "bug"),
			fixtureIssue("CLOSED", "Old bug", now.Add(-96*time.Hour)),
		},
		PageSize: 2,
	}
	filters := config.IssueFilters{ExcludeTitleRegexps: []*regexp.Regexp{
		regexp.MustCompile(`(?i)dependency dashboard`),
		regexp.MustCompile(`(?i)renovate bot`),
	}}
	snap := collectIssues(t, gql, filters)

	if got := len(gql.Requests()); got != 3 {
		t.Errorf("requests = %d, want 3 issue pages", got)
	}
	want := map[string]int64{"state=open": 2, "state=closed": 1, "label=bug": 2}
	if !maps.Equal(snap.counts, want) {
		t.Errorf("counts = %v, want %v", snap.counts, want)
	}
	// The excluded dashboard is the oldest open issue: age comes from the next one.
	assertAge(t, snap.age, now.Add(-24*time.Hour))
}

func TestIssueCollector_QueryError(t *testing.T) {
	gql := &collector.FixtureIssueClient{Err: errors.New("boom")}
	snap := collectIssues(t, gql, config.IssueFilters{})

	if fmt.Sprint(snap.failed) != "[org/repo]" {
		t.Errorf("failed = %v, want [org/repo]", snap.failed)
	}
	if len(snap.counts) != 0 || snap.age != -1 {
		t.Errorf("emitted %v, age %v for a failed repo, want nothing", snap.counts, snap.age)
	}
}
