package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"strings"

	"github.com/shurcooL/githubv4"
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

// dashboardMarkerRE matches text Renovate writes into a Dependency Dashboard
// (see lib/workers/repository/dependency-dashboard.ts in renovatebot/renovate
// 44.222.4): the default dependencyDashboardHeader, which users can override,
// and three markers a custom header does not remove: the no-branches notice,
// the Detected Dependencies heading, and a branch checkbox comment.
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

// dashboardTitle is the title Renovate gives the Dependency Dashboard issue.
const dashboardTitle = "Dependency Dashboard"

// dashboardNode is the GraphQL response shape for one candidate issue.
type dashboardNode struct {
	Title string
	Body  string
	// Author is nil when the author account was deleted.
	Author *struct {
		Typename string `graphql:"__typename" json:"__typename"`
		Login    string
	}
}

// dashboardRepo is one page of a repo's open issues created by
// $filterBy.createdBy, which takes the REST login ("renovate[bot]" for a
// GitHub App bot). $filterBy is nullable: nil means any author.
type dashboardRepo struct {
	Issues struct {
		Nodes    []dashboardNode
		PageInfo pageInfo
	} `graphql:"issues(first: 100, states: OPEN, filterBy: $filterBy, after: $cursor)"`
}

// dashboardQuery reads one repo's dashboardRepo.
type dashboardQuery struct {
	Repository dashboardRepo `graphql:"repository(owner: $owner, name: $name)"`
}

// dashboardBatch reads dashboardRepo for each repo of a batch, with the
// variables of dashboardQuery except owner and name.
var dashboardBatch = repoBatchQuery{
	selection: selectionOf(reflect.TypeFor[dashboardRepo]()),
	varDecls:  "$filterBy: IssueFilters, $cursor: String",
}

// DashboardCollector fetches the Renovate Dependency Dashboard issue and records queue metrics.
type DashboardCollector struct {
	gql       BatchGraphQLClient
	botLogin  string
	logger    *slog.Logger
	batchSize int
	limiter   *Limiter

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
// botLogin is the REST login of the Renovate bot account (e.g., "renovate[bot]").
func NewDashboardCollector(
	gql BatchGraphQLClient,
	botLogin string,
	meter metric.Meter,
	logger *slog.Logger,
	opts ...CollectorOption,
) (*DashboardCollector, error) {
	if logger == nil {
		logger = slog.Default()
	}
	o := applyOptions(opts)

	c := &DashboardCollector{
		gql:       gql,
		botLogin:  botLogin,
		logger:    logger,
		batchSize: o.batchSize,
		limiter:   o.limiter,
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

// Collect finds the Dependency Dashboard issue in each repo and records queue
// metrics, reading graphql_batch_size repos per request.
func (c *DashboardCollector) Collect(ctx context.Context, target string, repos []discovery.Repo) (*DashboardCollection, error) {
	coll := &DashboardCollection{}

	bs, found, errs := eachBatch(ctx, c.limiter, repos, c.batchSize, func(batch []discovery.Repo) ([]*dashboardNode, []error) {
		return c.findBatch(ctx, batch)
	})
	for b, batch := range bs {
		issues := found[b]
		for i, repo := range batch {
			if errs[b][i] != nil {
				c.logger.Error("dashboard collection failed",
					"target", target,
					"repo", repo.FullName,
					"err", errs[b][i],
				)
				coll.failed = append(coll.failed, repo.FullName)
				continue
			}
			if issues[i] == nil {
				// No dashboard issue found in this repo — skip.
				continue
			}
			coll.results = append(coll.results, DashboardCollectResult{
				target: target,
				org:    repo.Owner,
				repo:   repo.Name,
				stats:  parseDashboard(issues[i].Body),
			})
		}
	}

	return coll, nil
}

// filterBy returns the $filterBy variable: the bot's issues, or any author
// when no bot login is configured.
func (c *DashboardCollector) filterBy() *githubv4.IssueFilters {
	if c.botLogin == "" {
		return nil
	}
	login := githubv4.String(c.botLogin)
	return &githubv4.IssueFilters{CreatedBy: &login}
}

// findBatch looks for the Dependency Dashboard of each repo with one batched
// query, with the fallback rules of IssueCollector.countBatch. A nil issue
// with a nil error means the repo has no dashboard. It reads only fields set
// at construction, so batches can run concurrently.
func (c *DashboardCollector) findBatch(ctx context.Context, repos []discovery.Repo) ([]*dashboardNode, []error) {
	issues := make([]*dashboardNode, len(repos))
	errs := make([]error, len(repos))
	q := dashboardBatch
	q.vars = map[string]any{"filterBy": c.filterBy(), "cursor": nil}
	out := runRepoBatch(ctx, c.gql, q, repos)

	for i, repo := range repos {
		switch {
		case out.failed[i] != nil:
			errs[i] = fmt.Errorf("graphql dashboard query for %s: %w", repo.FullName, out.failed[i])
		case out.retry[i]:
			issues[i], errs[i] = c.findDashboardIssue(ctx, repo, nil)
		default:
			var r dashboardRepo
			if err := json.Unmarshal(out.data[i], &r); err != nil {
				errs[i] = fmt.Errorf("decoding dashboard query of %s: %w", repo.FullName, err)
				continue
			}
			var cursor *githubv4.String
			if issues[i], cursor = c.searchPage(&r); issues[i] == nil && cursor != nil {
				issues[i], errs[i] = c.findDashboardIssue(ctx, repo, cursor)
			}
		}
	}
	return issues, errs
}

// findDashboardIssue looks for the Renovate Dependency Dashboard among the
// open issues the bot created, from the page after cursor (the first page
// when nil): title == "Dependency Dashboard" AND author == botLogin.
// Filtering by creator at the source keeps pull requests and other authors'
// issues out, so this is one page in practice.
func (c *DashboardCollector) findDashboardIssue(ctx context.Context, repo discovery.Repo, cursor *githubv4.String) (*dashboardNode, error) {
	filterBy := c.filterBy()
	for {
		vars := map[string]interface{}{
			"owner":    githubv4.String(repo.Owner),
			"name":     githubv4.String(repo.Name),
			"filterBy": filterBy,
			"cursor":   cursor,
		}
		var q dashboardQuery
		if err := c.gql.Query(ctx, &q, vars); err != nil {
			return nil, fmt.Errorf("graphql dashboard query for %s: %w", repo.FullName, err)
		}

		iss, next := c.searchPage(&q.Repository)
		if iss != nil || next == nil {
			return iss, nil
		}
		cursor = next
	}
}

// searchPage returns the dashboard issue of one page, or the cursor of the
// next page when the page has none (nil on the last page).
func (c *DashboardCollector) searchPage(r *dashboardRepo) (*dashboardNode, *githubv4.String) {
	for i := range r.Issues.Nodes {
		if iss := &r.Issues.Nodes[i]; isDashboardIssue(iss, c.botLogin) {
			return iss, nil
		}
	}
	if !r.Issues.PageInfo.HasNextPage {
		return nil, nil
	}
	end := r.Issues.PageInfo.EndCursor
	return nil, &end
}

// isDashboardIssue returns true if the issue is the Renovate Dependency Dashboard.
func isDashboardIssue(iss *dashboardNode, botLogin string) bool {
	if iss.Title != dashboardTitle {
		return false
	}
	// Check author if botLogin is configured.
	if botLogin != "" {
		if iss.Author == nil || !strings.EqualFold(restLogin(iss.Author.Typename, iss.Author.Login), botLogin) {
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
