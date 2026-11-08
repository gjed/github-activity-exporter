package collector

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/shurcooL/githubv4"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/filter"
	"github.com/gjed/github-activity-exporter/internal/semconv"
)

// issueCountQuery reads a repo's issue counts without listing issues: the
// per-state totals, the oldest open issue and one page of labels, each with
// the number of issues carrying it. $states is nullable: nil means every state.
type issueCountQuery struct {
	Repository struct {
		Open   struct{ TotalCount int64 } `graphql:"open: issues(states: OPEN)"`
		Closed struct{ TotalCount int64 } `graphql:"closed: issues(states: CLOSED)"`
		Oldest struct {
			Nodes []struct{ CreatedAt time.Time }
		} `graphql:"oldest: issues(first: 1, states: OPEN, orderBy: {field: CREATED_AT, direction: ASC})"`
		Labels struct {
			Nodes []struct {
				Name   string
				Issues struct{ TotalCount int64 } `graphql:"issues(states: $states)"`
			}
			PageInfo pageInfo
		} `graphql:"labels(first: 100, after: $cursor)"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

// issueListQuery pages a repo's issues for the title filter. GitHub caps an
// issue at 100 labels, so labels(first: 100) reads all of them.
type issueListQuery struct {
	Repository struct {
		Issues struct {
			Nodes []struct {
				State     string // "OPEN" or "CLOSED"
				Title     string
				CreatedAt time.Time
				Labels    struct {
					Nodes []struct{ Name string }
				} `graphql:"labels(first: 100)"`
			}
			PageInfo pageInfo
		} `graphql:"issues(first: 100, states: $states, after: $cursor)"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

// pageInfo is the GraphQL connection paging state.
type pageInfo struct {
	HasNextPage bool
	EndCursor   githubv4.String
}

// IssueCollector reads GitHub issues via GraphQL, which unlike the REST
// issues list never returns pull requests, and records OTel metrics.
type IssueCollector struct {
	gql    GraphQLClient
	filter *filter.IssueFilter
	logger *slog.Logger

	// states is the GraphQL $states variable for filters.issues.states:
	// nil when unset (every state), empty when no valid state is listed.
	states *[]githubv4.IssueState
	// listIssues is set when title patterns are configured: counts then
	// depend on titles, so the issues are paged instead of counted.
	listIssues bool

	// OTel instruments.
	// issueCount matches the registry instrument (updowncounter) via its
	// observable variant so we can report a full snapshot each cycle.
	issueCount metric.Int64ObservableUpDownCounter
	issueAge   metric.Float64ObservableGauge
}

// issueStats aggregates per-repo issue data for observable gauge callbacks.
type issueStats struct {
	// countByState: state → count ("open", "closed")
	countByState map[string]int64
	// countByLabel: label → count
	countByLabel map[string]int64
	// oldestOpenAge is the age in seconds of the oldest open issue, or 0 if none.
	oldestOpenAge float64
}

// IssueCollectResult holds the per-repo stats after a Collect call.
type IssueCollectResult struct {
	target string
	org    string
	repo   string
	stats  issueStats
}

// IssueCollection holds results from one collection cycle.
type IssueCollection struct {
	results []IssueCollectResult

	// failed lists the FullName of repos whose collection failed this cycle.
	failed []string
}

// FailedRepos returns the FullName of every repo whose collection failed.
func (c *IssueCollection) FailedRepos() []string { return c.failed }

// NewIssueCollector creates an IssueCollector that records metrics on the given meter.
func NewIssueCollector(
	gql GraphQLClient,
	filterCfg config.IssueFilters,
	meter metric.Meter,
	logger *slog.Logger,
) (*IssueCollector, error) {
	if logger == nil {
		logger = slog.Default()
	}

	c := &IssueCollector{
		gql:        gql,
		filter:     filter.NewIssueFilter(filterCfg),
		logger:     logger,
		states:     issueStates(filterCfg.States),
		listIssues: len(filterCfg.ExcludeTitleRegexps) > 0,
	}

	var err error

	c.issueCount, err = meter.Int64ObservableUpDownCounter(semconv.MetricGitHubIssueCount,
		metric.WithDescription("Number of GitHub issues grouped by state and label."),
		metric.WithUnit("{issue}"),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricGitHubIssueCount, err)
	}

	c.issueAge, err = meter.Float64ObservableGauge(semconv.MetricGitHubIssueAge,
		metric.WithDescription("Age of the oldest open issue in seconds."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricGitHubIssueAge, err)
	}

	return c, nil
}

// Collect fetches issues for all repos and returns an IssueCollection.
func (c *IssueCollector) Collect(ctx context.Context, target string, repos []discovery.Repo) (*IssueCollection, error) {
	coll := &IssueCollection{}

	for _, repo := range repos {
		stats, err := c.collectRepo(ctx, target, repo)
		if err != nil {
			c.logger.Error("issue collection failed for repo",
				"target", target,
				"repo", repo.FullName,
				"err", err,
			)
			coll.failed = append(coll.failed, repo.FullName)
			continue
		}
		coll.results = append(coll.results, IssueCollectResult{
			target: target,
			org:    repo.Owner,
			repo:   repo.Name,
			stats:  stats,
		})
	}

	return coll, nil
}

// issueStates maps filters.issues.states to the GraphQL $states variable.
func issueStates(states []string) *[]githubv4.IssueState {
	if len(states) == 0 {
		return nil
	}
	out := []githubv4.IssueState{}
	for _, st := range []githubv4.IssueState{githubv4.IssueStateOpen, githubv4.IssueStateClosed} {
		if slices.ContainsFunc(states, func(s string) bool { return strings.EqualFold(s, string(st)) }) {
			out = append(out, st)
		}
	}
	return &out
}

// stateAllowed reports whether filters.issues.states lets issues in st through.
func (c *IssueCollector) stateAllowed(st githubv4.IssueState) bool {
	return c.states == nil || slices.Contains(*c.states, st)
}

// collectRepo computes the issue stats of a single repo.
func (c *IssueCollector) collectRepo(ctx context.Context, _ string, repo discovery.Repo) (issueStats, error) {
	stats := issueStats{
		countByState: make(map[string]int64),
		countByLabel: make(map[string]int64),
	}
	if c.states != nil && len(*c.states) == 0 {
		// No configured state is a valid issue state: nothing passes.
		return stats, nil
	}
	if c.listIssues {
		return stats, c.listRepo(ctx, repo, &stats)
	}
	return stats, c.countRepo(ctx, repo, &stats)
}

// countRepo fills stats from GraphQL totalCounts. Only valid without title
// patterns, which totalCount cannot apply. A state or label with no issue is
// left out, as when issues are listed.
func (c *IssueCollector) countRepo(ctx context.Context, repo discovery.Repo, stats *issueStats) error {
	var cursor *githubv4.String
	for {
		var q issueCountQuery
		if err := c.gql.Query(ctx, &q, c.vars(repo, cursor)); err != nil {
			return fmt.Errorf("graphql issue count query for %s: %w", repo.FullName, err)
		}
		r := &q.Repository

		if cursor == nil {
			if n := r.Open.TotalCount; n > 0 && c.stateAllowed(githubv4.IssueStateOpen) {
				stats.countByState["open"] = n
				if len(r.Oldest.Nodes) > 0 {
					stats.oldestOpenAge = time.Since(r.Oldest.Nodes[0].CreatedAt).Seconds()
				}
			}
			if n := r.Closed.TotalCount; n > 0 && c.stateAllowed(githubv4.IssueStateClosed) {
				stats.countByState["closed"] = n
			}
		}

		for _, l := range r.Labels.Nodes {
			if l.Issues.TotalCount > 0 {
				stats.countByLabel[l.Name] = l.Issues.TotalCount
			}
		}

		if !r.Labels.PageInfo.HasNextPage {
			return nil
		}
		end := r.Labels.PageInfo.EndCursor
		cursor = &end
	}
}

// listRepo pages the repo's issues and fills stats from those passing the
// issue filters.
func (c *IssueCollector) listRepo(ctx context.Context, repo discovery.Repo, stats *issueStats) error {
	var oldestOpenCreatedAt time.Time
	var cursor *githubv4.String
	for {
		var q issueListQuery
		if err := c.gql.Query(ctx, &q, c.vars(repo, cursor)); err != nil {
			return fmt.Errorf("graphql issue list query for %s: %w", repo.FullName, err)
		}

		for _, iss := range q.Repository.Issues.Nodes {
			state := strings.ToLower(iss.State) // "open" or "closed"

			labels := make([]string, 0, len(iss.Labels.Nodes))
			for _, l := range iss.Labels.Nodes {
				labels = append(labels, l.Name)
			}

			if !c.filter.Match(filter.Issue{State: state, Title: iss.Title, Labels: labels}) {
				continue
			}

			stats.countByState[state]++
			for _, l := range labels {
				stats.countByLabel[l]++
			}
			if state == "open" && (oldestOpenCreatedAt.IsZero() || iss.CreatedAt.Before(oldestOpenCreatedAt)) {
				oldestOpenCreatedAt = iss.CreatedAt
			}
		}

		if !q.Repository.Issues.PageInfo.HasNextPage {
			break
		}
		end := q.Repository.Issues.PageInfo.EndCursor
		cursor = &end
	}

	if !oldestOpenCreatedAt.IsZero() {
		stats.oldestOpenAge = time.Since(oldestOpenCreatedAt).Seconds()
	}
	return nil
}

// vars returns the variables of the issue queries.
func (c *IssueCollector) vars(repo discovery.Repo, cursor *githubv4.String) map[string]interface{} {
	return map[string]interface{}{
		"owner":  githubv4.String(repo.Owner),
		"name":   githubv4.String(repo.Name),
		"states": c.states,
		"cursor": cursor,
	}
}

// ObservableRegistration returns a metric.Callback for the issue gauge instruments.
func (c *IssueCollector) ObservableRegistration(coll *IssueCollection) metric.Callback {
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

			// issue.count by state
			for state, cnt := range r.stats.countByState {
				attrs := append(base, attribute.String(semconv.AttrGitHubIssueState, state)) //nolint:gocritic
				obs.ObserveInt64(c.issueCount, cnt, metric.WithAttributes(attrs...))
			}

			// issue.count by label
			for label, cnt := range r.stats.countByLabel {
				attrs := append(base, attribute.String(semconv.AttrGitHubIssueLabel, label)) //nolint:gocritic
				obs.ObserveInt64(c.issueCount, cnt, metric.WithAttributes(attrs...))
			}

			// issue.age (oldest open)
			if r.stats.oldestOpenAge > 0 {
				obs.ObserveFloat64(c.issueAge, r.stats.oldestOpenAge,
					metric.WithAttributes(base...))
			}
		}
		return nil
	}
}

// ObservableInstruments returns the observable instruments for batch RegisterCallback.
func (c *IssueCollector) ObservableInstruments() []metric.Observable {
	return []metric.Observable{c.issueCount, c.issueAge}
}
