package collector

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/go-github/v93/github"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/semconv"
)

// dashboardSectionRE matches a known Renovate Dependency Dashboard section
// header. The capturing group holds the section name.
var dashboardSectionRE = regexp.MustCompile(
	`(?m)^## (Awaiting Schedule|Rate-Limited|Pending Approval|Open)\s*$`,
)

// dashboardMarkerRE matches text that Renovate always writes into a Dependency
// Dashboard regardless of user configuration (see
// lib/workers/repository/dependency-dashboard.ts in renovatebot/renovate 44.222.4):
// the default dependencyDashboardHeader, the no-branches notice, the Detected
// Dependencies heading, or a branch checkbox comment.
var dashboardMarkerRE = regexp.MustCompile(
	`This issue lists Renovate updates|This repository currently has no open or pending branches\.|(?m:^## Detected Dependencies\s*$)|<!-- [a-zA-Z]+-branch=\S+ -->`,
)

// anyH2RE matches any level-2 Markdown heading. Used as a generic boundary
// so that unknown/future sections don't bleed into the previous known section.
var anyH2RE = regexp.MustCompile(`(?m)^## .*$`)

// branchCheckboxRE matches a checked or unchecked dashboard item that references
// a branch, mirroring Renovate's own `<!-- <type>-branch=<name> -->` markup.
// Bulk actions (e.g. create-all-rate-limited-prs) carry no -branch= and are
// not counted.
var branchCheckboxRE = regexp.MustCompile(`(?m)^\s*- \[[ xX]\] <!-- [a-zA-Z]+-branch=\S+ -->`)

// sectionNameToSemconv maps the Markdown section header to the semconv attribute value.
var sectionNameToSemconv = map[string]string{
	"Awaiting Schedule": semconv.AttrRenovateDashboardSectionAwaitingSchedule,
	"Rate-Limited":      semconv.AttrRenovateDashboardSectionRateLimited,
	"Pending Approval":  semconv.AttrRenovateDashboardSectionPendingApproval,
	"Open":              semconv.AttrRenovateDashboardSectionPending, // "Open" section maps to "pending"
}

// DashboardIssueClient abstracts the GitHub REST API issue listing for the dashboard collector.
type DashboardIssueClient interface {
	ListByRepo(ctx context.Context, owner, repo string, opts *github.IssueListByRepoOptions) ([]*github.Issue, *github.Response, error)
}

// DashboardCollector fetches the Renovate Dependency Dashboard issue and records queue metrics.
type DashboardCollector struct {
	rest     DashboardIssueClient
	botLogin string
	logger   *slog.Logger

	// OTel instruments
	queueGauge      metric.Int64ObservableGauge
	parseErrorGauge metric.Int64ObservableGauge
}

// dashboardStats holds the parsed queue state for one repo's dashboard.
type dashboardStats struct {
	// sections maps semconv section value → count of list items
	sections   map[string]int64
	parseError int64 // 1 if the body is not a Renovate dashboard, 0 otherwise
}

// DashboardCollectResult holds per-repo dashboard stats.
type DashboardCollectResult struct {
	target string
	org    string
	repo   string
	stats  dashboardStats
}

// DashboardCollection holds results from one collection cycle.
type DashboardCollection struct {
	results []DashboardCollectResult

	// failed lists the FullName of repos whose collection failed this cycle.
	failed []string
}

// FailedRepos returns the FullName of every repo whose collection failed.
func (c *DashboardCollection) FailedRepos() []string { return c.failed }

// NewDashboardCollector creates a DashboardCollector.
// botLogin is the GitHub username of the Renovate bot account (e.g., "renovate[bot]").
func NewDashboardCollector(
	rest DashboardIssueClient,
	botLogin string,
	meter metric.Meter,
	logger *slog.Logger,
) (*DashboardCollector, error) {
	if logger == nil {
		logger = slog.Default()
	}

	c := &DashboardCollector{
		rest:     rest,
		botLogin: botLogin,
		logger:   logger,
	}

	var err error

	c.queueGauge, err = meter.Int64ObservableGauge(semconv.MetricRenovateDashboardQueue,
		metric.WithDescription("Renovate Dependency Dashboard queue per section."),
		metric.WithUnit("{pr}"),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricRenovateDashboardQueue, err)
	}

	c.parseErrorGauge, err = meter.Int64ObservableGauge(semconv.MetricRenovateDashboardParseError,
		metric.WithDescription("1 when the Renovate dashboard cannot be parsed, 0 on success."),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricRenovateDashboardParseError, err)
	}

	return c, nil
}

// Collect finds the Dependency Dashboard issue in each repo and records queue metrics.
func (c *DashboardCollector) Collect(ctx context.Context, target string, repos []discovery.Repo) (*DashboardCollection, error) {
	coll := &DashboardCollection{}

	for _, repo := range repos {
		stats, err := c.collectRepo(ctx, repo)
		if err != nil {
			c.logger.Error("dashboard collection failed",
				"target", target,
				"repo", repo.FullName,
				"err", err,
			)
			coll.failed = append(coll.failed, repo.FullName)
			continue
		}
		if stats == nil {
			// No dashboard issue found in this repo — skip.
			continue
		}
		coll.results = append(coll.results, DashboardCollectResult{
			target: target,
			org:    repo.Owner,
			repo:   repo.Name,
			stats:  *stats,
		})
	}

	return coll, nil
}

// collectRepo finds and parses the Dependency Dashboard issue for a single repo.
// Returns nil stats (no error) if no dashboard issue is found.
func (c *DashboardCollector) collectRepo(ctx context.Context, repo discovery.Repo) (*dashboardStats, error) {
	issue, err := c.findDashboardIssue(ctx, repo)
	if err != nil {
		return nil, err
	}
	if issue == nil {
		return nil, nil
	}

	stats := parseDashboard(issue.GetBody())
	return &stats, nil
}

// findDashboardIssue scans open issues for the repo looking for the Renovate
// Dependency Dashboard: title == "Dependency Dashboard" AND author == botLogin.
func (c *DashboardCollector) findDashboardIssue(ctx context.Context, repo discovery.Repo) (*github.Issue, error) {
	opts := &github.IssueListByRepoOptions{
		State:       "open",
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		page, resp, err := c.rest.ListByRepo(ctx, repo.Owner, repo.Name, opts)
		if err != nil {
			return nil, fmt.Errorf("list issues for %s: %w", repo.FullName, err)
		}

		for _, iss := range page {
			if isDashboardIssue(iss, c.botLogin) {
				return iss, nil
			}
		}

		if resp.NextPage == 0 {
			break
		}
		opts.ListOptions.Page = resp.NextPage
	}

	return nil, nil
}

// isDashboardIssue returns true if the issue is the Renovate Dependency Dashboard.
func isDashboardIssue(iss *github.Issue, botLogin string) bool {
	if iss == nil {
		return false
	}
	if iss.GetTitle() != "Dependency Dashboard" {
		return false
	}
	// Check author if botLogin is configured.
	if botLogin != "" {
		if iss.User == nil || !strings.EqualFold(iss.User.GetLogin(), botLogin) {
			return false
		}
	}
	return true
}

// parseDashboard parses a Renovate Dependency Dashboard issue body and returns
// queue counts per section. parseError is set to 1 only when the body carries no
// Renovate dashboard marker. Every tracked section is reported; a section
// absent from the body counts 0.
//
// Any `##` heading is treated as a section boundary. This prevents items under
// unknown/future sections from leaking into a preceding known section's count.
func parseDashboard(body string) dashboardStats {
	stats := dashboardStats{
		sections: make(map[string]int64),
	}

	if !dashboardMarkerRE.MatchString(body) {
		stats.parseError = 1
		return stats
	}

	for _, semconvVal := range sectionNameToSemconv {
		stats.sections[semconvVal] = 0
	}

	// Collect all ## headings as boundaries (both known and unknown).
	allBoundaries := anyH2RE.FindAllStringIndex(body, -1)

	for i, m := range allBoundaries {
		headerLine := body[m[0]:m[1]]

		// Only record metrics for known sections.
		sub := dashboardSectionRE.FindStringSubmatch(headerLine)
		if len(sub) < 2 {
			continue
		}
		sectionName := sub[1]
		semconvVal, ok := sectionNameToSemconv[sectionName]
		if !ok {
			continue
		}

		// Section body runs from the end of this heading to the start of the
		// next ## heading (or EOF). Unknown headings act as boundaries even
		// though we don't record metrics for them.
		sectionStart := m[1]
		var sectionEnd int
		if i+1 < len(allBoundaries) {
			sectionEnd = allBoundaries[i+1][0]
		} else {
			sectionEnd = len(body)
		}

		sectionBody := body[sectionStart:sectionEnd]
		count := int64(len(branchCheckboxRE.FindAllString(sectionBody, -1)))
		stats.sections[semconvVal] = count
	}

	return stats
}

// ObservableRegistration returns a metric.Callback for dashboard gauge instruments.
func (c *DashboardCollector) ObservableRegistration(coll *DashboardCollection) metric.Callback {
	return func(_ context.Context, obs metric.Observer) error {
		if coll == nil {
			return nil
		}
		for _, r := range coll.results {
			base := []attribute.KeyValue{
				attribute.String(semconv.AttrExporterTarget, r.target),
				attribute.String(semconv.AttrGitHubOrg, r.org),
				attribute.String(semconv.AttrGitHubRepo, r.repo),
			}

			// parse_error gauge
			obs.ObserveInt64(c.parseErrorGauge, r.stats.parseError,
				metric.WithAttributes(base...))

			// queue per section
			for section, cnt := range r.stats.sections {
				attrs := append(base, attribute.String(semconv.AttrRenovateDashboardSection, section)) //nolint:gocritic
				obs.ObserveInt64(c.queueGauge, cnt, metric.WithAttributes(attrs...))
			}
		}
		return nil
	}
}

// ObservableInstruments returns the observable instruments for batch RegisterCallback.
func (c *DashboardCollector) ObservableInstruments() []metric.Observable {
	return []metric.Observable{c.queueGauge, c.parseErrorGauge}
}
