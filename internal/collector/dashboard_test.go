package collector_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-github/v62/github"

	"github.com/gjed/renovate-exporter/internal/collector"
	"github.com/gjed/renovate-exporter/internal/discovery"
	"github.com/gjed/renovate-exporter/internal/semconv"
)

// fakeDashboardClient implements DashboardIssueClient using fixture issues.
type fakeDashboardClient struct {
	issues []*github.Issue
}

func (f *fakeDashboardClient) ListByRepo(_ context.Context, _, _ string, _ *github.IssueListByRepoOptions) ([]*github.Issue, *github.Response, error) {
	return f.issues, &github.Response{}, nil
}

func dashboardIssue(title, body, author string) *github.Issue {
	state := "open"
	n := 1
	ts := github.Timestamp{Time: time.Now()}
	return &github.Issue{
		Number:    &n,
		State:     &state,
		Title:     &title,
		Body:      &body,
		CreatedAt: &ts,
		User:      &github.User{Login: &author},
	}
}

const typicalDashboard = `
This issue contains a list of Renovate updates and their statuses.

## Awaiting Schedule
- [ ] Update dependency foo to v2.0.0
- [ ] Update dependency bar to v1.5.0

## Rate-Limited
- [x] Update dependency baz to v3.0.0

## Pending Approval
- [ ] Update dependency qux to v4.0.0
- [ ] Update dependency quux to v5.0.0
- [ ] Update dependency corge to v6.0.0

## Open
- [ ] Update group "node" packages
`

const emptyDashboard = `
## Awaiting Schedule

## Rate-Limited

## Pending Approval

## Open
`

const noSectionsDashboard = `
This issue has no recognised sections.
Some text here.
`

const extraSectionsDashboard = `
## Awaiting Schedule
- [ ] foo

## Unknown Section
- [ ] should not count

## Open
- [ ] bar
- [ ] baz
`

func TestDashboardCollector_TypicalDashboard(t *testing.T) {
	issues := []*github.Issue{
		dashboardIssue("Dependency Dashboard", typicalDashboard, "renovate[bot]"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&fakeDashboardClient{issues: issues},
		"renovate[bot]",
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewDashboardCollector: %v", err)
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
	dps := findGaugeInt(t, rm, semconv.MetricRenovateDashboardQueue)

	sectionMap := make(map[string]int64)
	for _, dp := range dps {
		if v, ok := dp.Attributes.Value(semconv.AttrRenovateDashboardSection); ok {
			sectionMap[v.AsString()] = dp.Value
		}
	}

	tests := []struct {
		section string
		want    int64
	}{
		{semconv.AttrRenovateDashboardSectionAwaitingSchedule, 2},
		{semconv.AttrRenovateDashboardSectionRateLimited, 1},
		{semconv.AttrRenovateDashboardSectionPendingApproval, 3},
		{semconv.AttrRenovateDashboardSectionPending, 1}, // "Open" → pending
	}
	for _, tc := range tests {
		if got := sectionMap[tc.section]; got != tc.want {
			t.Errorf("section %q = %d, want %d", tc.section, got, tc.want)
		}
	}

	// parse_error should be 0
	errDps := findGaugeInt(t, rm, semconv.MetricRenovateDashboardParseError)
	for _, dp := range errDps {
		if dp.Value != 0 {
			t.Errorf("parse_error = %d, want 0", dp.Value)
		}
	}
}

func TestDashboardCollector_EmptySections(t *testing.T) {
	issues := []*github.Issue{
		dashboardIssue("Dependency Dashboard", emptyDashboard, "renovate[bot]"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&fakeDashboardClient{issues: issues},
		"renovate[bot]",
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewDashboardCollector: %v", err)
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
	dps := findGaugeInt(t, rm, semconv.MetricRenovateDashboardQueue)

	for _, dp := range dps {
		if dp.Value != 0 {
			t.Errorf("empty section should have count 0, got %d", dp.Value)
		}
	}
}

func TestDashboardCollector_NoSections_ParseError(t *testing.T) {
	issues := []*github.Issue{
		dashboardIssue("Dependency Dashboard", noSectionsDashboard, "renovate[bot]"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&fakeDashboardClient{issues: issues},
		"renovate[bot]",
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewDashboardCollector: %v", err)
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
	errDps := findGaugeInt(t, rm, semconv.MetricRenovateDashboardParseError)

	var maxErr int64
	for _, dp := range errDps {
		if dp.Value > maxErr {
			maxErr = dp.Value
		}
	}
	if maxErr != 1 {
		t.Errorf("parse_error = %d, want 1", maxErr)
	}
}

func TestDashboardCollector_ExtraSections_IgnoredGracefully(t *testing.T) {
	issues := []*github.Issue{
		dashboardIssue("Dependency Dashboard", extraSectionsDashboard, "renovate[bot]"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&fakeDashboardClient{issues: issues},
		"renovate[bot]",
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewDashboardCollector: %v", err)
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
	dps := findGaugeInt(t, rm, semconv.MetricRenovateDashboardQueue)

	sectionMap := make(map[string]int64)
	for _, dp := range dps {
		if v, ok := dp.Attributes.Value(semconv.AttrRenovateDashboardSection); ok {
			sectionMap[v.AsString()] = dp.Value
		}
	}

	// "Awaiting Schedule" has 1 item. The "## Unknown Section" is treated as a
	// boundary (any ## heading terminates the previous section's body) so the
	// item under it does NOT bleed into Awaiting Schedule's count.
	if sectionMap[semconv.AttrRenovateDashboardSectionAwaitingSchedule] != 1 {
		t.Errorf("awaiting-schedule = %d, want 1", sectionMap[semconv.AttrRenovateDashboardSectionAwaitingSchedule])
	}
	if sectionMap[semconv.AttrRenovateDashboardSectionPending] != 2 {
		t.Errorf("open (pending) = %d, want 2", sectionMap[semconv.AttrRenovateDashboardSectionPending])
	}
}

func TestDashboardCollector_NonDashboardIssue_Skipped(t *testing.T) {
	// Issue has wrong title — should not trigger dashboard collection
	issues := []*github.Issue{
		dashboardIssue("Not the dashboard", typicalDashboard, "renovate[bot]"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&fakeDashboardClient{issues: issues},
		"renovate[bot]",
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewDashboardCollector: %v", err)
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
	// No dashboard data should be recorded
	dps := findGaugeInt(t, rm, semconv.MetricRenovateDashboardQueue)
	if len(dps) != 0 {
		t.Errorf("expected no queue data points for non-dashboard issue, got %d", len(dps))
	}
}

func TestDashboardCollector_WrongBotAuthor_Skipped(t *testing.T) {
	issues := []*github.Issue{
		dashboardIssue("Dependency Dashboard", typicalDashboard, "other-user"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&fakeDashboardClient{issues: issues},
		"renovate[bot]",
		meter,
		nil,
	)
	if err != nil {
		t.Fatalf("NewDashboardCollector: %v", err)
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
	dps := findGaugeInt(t, rm, semconv.MetricRenovateDashboardQueue)
	if len(dps) != 0 {
		t.Errorf("expected no data points for wrong bot author, got %d", len(dps))
	}
}
