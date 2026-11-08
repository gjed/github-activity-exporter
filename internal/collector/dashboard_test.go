package collector_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/semconv"
)

// dashboardIssue returns an open fixture issue. A REST-form bot author
// ("renovate[bot]") is stored the way GraphQL returns it: login "renovate",
// __typename Bot.
func dashboardIssue(title, body, author string) collector.ExportedIssueNode {
	login, isBot := strings.CutSuffix(author, "[bot]")
	return collector.ExportedIssueNode{
		State:       "OPEN",
		Title:       title,
		Body:        body,
		CreatedAt:   time.Now(),
		Author:      login,
		AuthorIsBot: isBot,
	}
}

const typicalDashboard = `
This issue lists Renovate updates and detected dependencies.

## Awaiting Schedule
- [ ] <!-- unschedule-branch=renovate/update-dependency-foo-to-v2-0-0 -->Update dependency foo to v2.0.0
- [ ] <!-- unschedule-branch=renovate/update-dependency-bar-to-v1-5-0 -->Update dependency bar to v1.5.0

## Rate-Limited
- [x] <!-- unlimit-branch=renovate/update-dependency-baz-to-v3-0-0 -->Update dependency baz to v3.0.0

## Pending Approval
- [ ] <!-- approve-branch=renovate/update-dependency-qux-to-v4-0-0 -->Update dependency qux to v4.0.0
- [ ] <!-- approve-branch=renovate/update-dependency-quux-to-v5-0-0 -->Update dependency quux to v5.0.0
- [ ] <!-- approve-branch=renovate/update-dependency-corge-to-v6-0-0 -->Update dependency corge to v6.0.0

## Open
- [ ] <!-- rebase-branch=renovate/update-group-node-packages -->Update group "node" packages
`

const emptyDashboard = `
This issue lists Renovate updates and detected dependencies.
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
This issue lists Renovate updates and detected dependencies.
## Awaiting Schedule
- [ ] <!-- unschedule-branch=renovate/foo -->foo

## Unknown Section
- [ ] <!-- rebase-branch=renovate/should-not-count -->should not count

## Open
- [ ] <!-- rebase-branch=renovate/bar -->bar
- [ ] <!-- rebase-branch=renovate/baz -->baz
`

func TestDashboardCollector_TypicalDashboard(t *testing.T) {
	issues := []collector.ExportedIssueNode{
		dashboardIssue("Dependency Dashboard", typicalDashboard, "renovate[bot]"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&collector.FixtureIssueClient{Issues: issues},
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
	issues := []collector.ExportedIssueNode{
		dashboardIssue("Dependency Dashboard", emptyDashboard, "renovate[bot]"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&collector.FixtureIssueClient{Issues: issues},
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
	issues := []collector.ExportedIssueNode{
		dashboardIssue("Dependency Dashboard", noSectionsDashboard, "renovate[bot]"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&collector.FixtureIssueClient{Issues: issues},
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
	issues := []collector.ExportedIssueNode{
		dashboardIssue("Dependency Dashboard", extraSectionsDashboard, "renovate[bot]"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&collector.FixtureIssueClient{Issues: issues},
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
	issues := []collector.ExportedIssueNode{
		dashboardIssue("Not the dashboard", typicalDashboard, "renovate[bot]"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&collector.FixtureIssueClient{Issues: issues},
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
	issues := []collector.ExportedIssueNode{
		dashboardIssue("Dependency Dashboard", typicalDashboard, "other-user"),
	}

	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(
		&collector.FixtureIssueClient{Issues: issues},
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

// collectDashboard runs the dashboard collector once and returns the emitted
// queue per section and the failed repos.
func collectDashboard(t *testing.T, gql collector.GraphQLClient, botLogin string) (map[string]int64, []string) {
	t.Helper()
	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")

	coll, err := collector.NewDashboardCollector(gql, botLogin, meter, nil)
	if err != nil {
		t.Fatalf("NewDashboardCollector: %v", err)
	}
	result, err := coll.Collect(context.Background(), "tgt", []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, err := meter.RegisterCallback(coll.ObservableRegistration(result), coll.ObservableInstruments()...); err != nil {
		t.Fatalf("RegisterCallback: %v", err)
	}

	queue := map[string]int64{}
	for _, dp := range findGaugeInt(t, collectMetrics(t, reader), semconv.MetricRenovateDashboardQueue) {
		v, _ := dp.Attributes.Value(semconv.AttrRenovateDashboardSection)
		queue[v.AsString()] = dp.Value
	}
	return queue, result.FailedRepos()
}

func TestDashboardCollector_OneRequestWithManyPRs(t *testing.T) {
	prs := make([]collector.ExportedPRNode, 500)
	for i := range prs {
		prs[i] = collector.ExportedPRNode{Number: i + 1, State: "OPEN", CreatedAt: time.Now(), Author: "renovate", AuthorIsBot: true}
	}
	issues := []collector.ExportedIssueNode{
		dashboardIssue("Action Required: Fix Renovate Configuration", "", "renovate[bot]"),
		dashboardIssue("Dependency Dashboard", typicalDashboard, "someone-else"),
		dashboardIssue("Dependency Dashboard", typicalDashboard, "renovate[bot]"),
	}
	for i := range 300 {
		issues = append(issues, dashboardIssue(fmt.Sprintf("Bug %d", i), "", "user"))
	}
	gql := &collector.FixtureIssueClient{Issues: issues, PullRequests: prs}

	queue, failed := collectDashboard(t, gql, "renovate[bot]")

	want := []collector.FixtureIssueRequest{{Query: "dashboard", CreatedBy: "renovate[bot]"}}
	if got := gql.Requests(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("requests = %+v, want %+v", got, want)
	}
	if len(failed) != 0 || queue[semconv.AttrRenovateDashboardSectionPendingApproval] != 3 {
		t.Errorf("queue = %v, failed = %v, want pending-approval 3 and no failure", queue, failed)
	}
}

// TestDashboardCollector_BotLoginFormats covers the REST/GraphQL login
// difference: the configured login is the REST form, sent as-is to createdBy;
// GraphQL returns a GitHub App bot as "renovate" with __typename Bot.
func TestDashboardCollector_BotLoginFormats(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		author     string // REST form, see dashboardIssue
		found      bool
	}{
		{"app bot", "renovate[bot]", "renovate[bot]", true},
		{"app bot, other case", "Renovate[bot]", "renovate[bot]", true},
		{"user account", "zxbot", "ZxBot", true},
		{"bare app login", "renovate", "renovate[bot]", false},
		{"user named like the app", "renovate[bot]", "renovate", false},
		{"no login configured", "", "anyone", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gql := &collector.FixtureIssueClient{Issues: []collector.ExportedIssueNode{
				dashboardIssue("Dependency Dashboard", typicalDashboard, tt.author),
			}}
			queue, failed := collectDashboard(t, gql, tt.configured)

			reqs := gql.Requests()
			if len(reqs) != 1 || reqs[0].CreatedBy != tt.configured {
				t.Errorf("requests = %+v, want one with createdBy %q", reqs, tt.configured)
			}
			if got := len(queue) > 0; got != tt.found || len(failed) != 0 {
				t.Errorf("found = %v (failed %v), want %v", got, failed, tt.found)
			}
		})
	}
}

func TestDashboardCollector_NoDashboardIssue(t *testing.T) {
	gql := &collector.FixtureIssueClient{Issues: []collector.ExportedIssueNode{
		dashboardIssue("Action Required: Fix Renovate Configuration", "", "renovate[bot]"),
	}}
	queue, failed := collectDashboard(t, gql, "renovate[bot]")
	if len(queue) != 0 || len(failed) != 0 {
		t.Errorf("queue = %v, failed = %v, want neither", queue, failed)
	}
}

func TestDashboardCollector_QueryError(t *testing.T) {
	gql := &collector.FixtureIssueClient{Err: errors.New("boom")}
	queue, failed := collectDashboard(t, gql, "renovate[bot]")
	if len(queue) != 0 || fmt.Sprint(failed) != "[org/repo]" {
		t.Errorf("queue = %v, failed = %v, want no queue and [org/repo]", queue, failed)
	}
}

func TestDashboardCollector_Pages(t *testing.T) {
	gql := &collector.FixtureIssueClient{
		Issues: []collector.ExportedIssueNode{
			dashboardIssue("Action Required: Fix Renovate Configuration", "", "renovate[bot]"),
			dashboardIssue("Dependency Dashboard", typicalDashboard, "renovate[bot]"),
		},
		PageSize: 1,
	}
	queue, _ := collectDashboard(t, gql, "renovate[bot]")

	reqs := gql.Requests()
	if len(reqs) != 2 || reqs[1].Cursor != "1" {
		t.Errorf("requests = %+v, want 2 pages", reqs)
	}
	if len(queue) == 0 {
		t.Error("dashboard on the second page not found")
	}
}
