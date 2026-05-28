package collector

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/go-github/v62/github"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/discovery"
	"github.com/gjed/renovate-exporter/internal/filter"
	"github.com/gjed/renovate-exporter/internal/semconv"
)

// IssueListClient abstracts the GitHub REST API issue listing call (injectable in tests).
type IssueListClient interface {
	ListByRepo(ctx context.Context, owner, repo string, opts *github.IssueListByRepoOptions) ([]*github.Issue, *github.Response, error)
}

// IssueCollector fetches GitHub issues via the REST API and records OTel metrics.
type IssueCollector struct {
	rest   IssueListClient
	filter *filter.IssueFilter
	logger *slog.Logger

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
}

// NewIssueCollector creates an IssueCollector that records metrics on the given meter.
func NewIssueCollector(
	rest IssueListClient,
	filterCfg config.IssueFilters,
	meter metric.Meter,
	logger *slog.Logger,
) (*IssueCollector, error) {
	if logger == nil {
		logger = slog.Default()
	}

	c := &IssueCollector{
		rest:   rest,
		filter: filter.NewIssueFilter(filterCfg),
		logger: logger,
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

// collectRepo fetches and aggregates issues for a single repo.
func (c *IssueCollector) collectRepo(ctx context.Context, _ string, repo discovery.Repo) (issueStats, error) {
	issues, err := c.fetchIssues(ctx, repo)
	if err != nil {
		return issueStats{}, err
	}

	stats := issueStats{
		countByState: make(map[string]int64),
		countByLabel: make(map[string]int64),
	}

	var oldestOpenCreatedAt time.Time

	for _, iss := range issues {
		// The GitHub REST API returns pull requests in the issues list.
		// Skip them — PR metrics are handled by PRCollector.
		if iss.IsPullRequest() {
			continue
		}

		state := iss.GetState() // "open" or "closed"
		title := iss.GetTitle()

		labels := make([]string, 0, len(iss.Labels))
		for _, l := range iss.Labels {
			labels = append(labels, l.GetName())
		}

		fi := filter.Issue{State: state, Title: title, Labels: labels}
		if !c.filter.Match(fi) {
			continue
		}

		stats.countByState[state]++

		for _, l := range labels {
			stats.countByLabel[l]++
		}

		if state == "open" {
			if iss.CreatedAt != nil {
				createdAt := iss.CreatedAt.Time
				if oldestOpenCreatedAt.IsZero() || createdAt.Before(oldestOpenCreatedAt) {
					oldestOpenCreatedAt = createdAt
				}
			}
		}
	}

	if !oldestOpenCreatedAt.IsZero() {
		stats.oldestOpenAge = time.Since(oldestOpenCreatedAt).Seconds()
	}

	return stats, nil
}

// fetchIssues paginates the REST issues endpoint for a repo.
func (c *IssueCollector) fetchIssues(ctx context.Context, repo discovery.Repo) ([]*github.Issue, error) {
	var all []*github.Issue

	opts := &github.IssueListByRepoOptions{
		State: "all",
		ListOptions: github.ListOptions{PerPage: 100},
	}

	for {
		page, resp, err := c.rest.ListByRepo(ctx, repo.Owner, repo.Name, opts)
		if err != nil {
			return nil, fmt.Errorf("list issues for %s: %w", repo.FullName, err)
		}
		all = append(all, page...)
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return all, nil
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
