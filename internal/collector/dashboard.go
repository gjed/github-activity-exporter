package collector

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/go-github/v62/github"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/renovate-exporter/internal/discovery"
	"github.com/gjed/renovate-exporter/internal/semconv"
)

// dashboardSectionRE matches the section header lines in a Renovate Dependency
// Dashboard issue body. The capturing group holds the normalised section name.
var dashboardSectionRE = regexp.MustCompile(
	`(?m)^## (Awaiting Schedule|Rate-Limited|Pending Approval|Open)\s*$`,
)

// checkboxRE matches both checked and unchecked Markdown list items.
var checkboxRE = regexp.MustCompile(`(?m)^\s*- \[[ x]\]`)

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
	sections    map[string]int64
	parseError  int64 // 1 if no sections found, 0 on success
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
}

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
		State: "open",
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
		opts.Page = resp.NextPage
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
// queue counts per section. If no expected sections are found, parseError is set to 1.
func parseDashboard(body string) dashboardStats {
	stats := dashboardStats{
		sections: make(map[string]int64),
	}

	matches := dashboardSectionRE.FindAllStringIndex(body, -1)
	if len(matches) == 0 {
		stats.parseError = 1
		return stats
	}

	// For each section match, count checkboxes until the next section or EOF.
	for i, m := range matches {
		headerLine := body[m[0]:m[1]]
		// Extract section name from the match.
		sub := dashboardSectionRE.FindStringSubmatch(headerLine)
		if len(sub) < 2 {
			continue
		}
		sectionName := sub[1]
		semconvVal, ok := sectionNameToSemconv[sectionName]
		if !ok {
			continue
		}

		// Determine the body slice for this section.
		sectionStart := m[1]
		var sectionEnd int
		if i+1 < len(matches) {
			sectionEnd = matches[i+1][0]
		} else {
			sectionEnd = len(body)
		}

		sectionBody := body[sectionStart:sectionEnd]
		count := int64(len(checkboxRE.FindAllString(sectionBody, -1)))
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
