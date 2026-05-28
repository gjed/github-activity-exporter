// Package collector implements the GitHub data collectors that fetch PR and
// issue state and record OTel metrics.
package collector

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/shurcooL/githubv4"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/discovery"
	"github.com/gjed/renovate-exporter/internal/filter"
	"github.com/gjed/renovate-exporter/internal/semconv"
)

const (
	defaultMaxPRsPerRepo = 500
	defaultLookbackDays  = 30
)

// GraphQLClient is the minimal interface used by PRCollector (satisfied by
// *githubv4.Client).
type GraphQLClient interface {
	Query(ctx context.Context, q interface{}, variables map[string]interface{}) error
}

// prNode is the GraphQL response shape for a single PR node.
type prNode struct {
	Number    int
	State     string // "OPEN", "CLOSED", "MERGED"
	IsDraft   bool
	CreatedAt time.Time
	MergedAt  *time.Time
	ClosedAt  *time.Time
	Labels    struct {
		Nodes []struct{ Name string }
	} `graphql:"labels(first: 20)"`
	Reviews struct {
		Nodes []struct{ State string }
	} `graphql:"reviews(first: 50)"`
	ReviewDecision *string
}

// prQuery is the paginated GraphQL v4 query for pull requests.
type prQuery struct {
	Repository struct {
		PullRequests struct {
			Nodes    []prNode
			PageInfo struct {
				HasNextPage bool
				EndCursor   githubv4.String
			}
		} `graphql:"pullRequests(first: 100, states: [OPEN, CLOSED, MERGED], after: $cursor)"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

// PRCollectorConfig holds configurable limits for the PR collector.
type PRCollectorConfig struct {
	MaxPRsPerRepo int
	LookbackDays  int
}

// PRCollector fetches PRs via GraphQL and records OTel metrics.
type PRCollector struct {
	gql    GraphQLClient
	filter *filter.PRFilter
	cfg    PRCollectorConfig
	logger *slog.Logger

	// OTel instruments.
	// pr.count and issue.count are updowncounters in the registry; the
	// observable variant lets us report a full snapshot each cycle without
	// needing to track deltas manually.
	prCount      metric.Int64ObservableUpDownCounter
	prAge        metric.Float64ObservableGauge
	closeDur     metric.Float64Histogram
	automerged   metric.Int64Counter
	reviewStatus metric.Int64ObservableGauge
}

// NewPRCollector creates a PRCollector that records metrics on the given meter.
func NewPRCollector(
	gql GraphQLClient,
	filterCfg config.PRFilters,
	cfg PRCollectorConfig,
	meter metric.Meter,
	logger *slog.Logger,
) (*PRCollector, error) {
	if cfg.MaxPRsPerRepo <= 0 {
		cfg.MaxPRsPerRepo = defaultMaxPRsPerRepo
	}
	if cfg.LookbackDays <= 0 {
		cfg.LookbackDays = defaultLookbackDays
	}
	if logger == nil {
		logger = slog.Default()
	}

	c := &PRCollector{
		gql:    gql,
		filter: filter.NewPRFilter(filterCfg),
		cfg:    cfg,
		logger: logger,
	}

	var err error

	c.prCount, err = meter.Int64ObservableUpDownCounter(semconv.MetricGitHubPrCount,
		metric.WithDescription("Number of pull requests grouped by state and label."),
		metric.WithUnit("{pr}"),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricGitHubPrCount, err)
	}

	c.prAge, err = meter.Float64ObservableGauge(semconv.MetricGitHubPrAge,
		metric.WithDescription("Age of the oldest open PR in seconds."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricGitHubPrAge, err)
	}

	c.closeDur, err = meter.Float64Histogram(semconv.MetricGitHubPrCloseDuration,
		metric.WithDescription("Time from PR creation to close in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(
			3600, 7200, 14400, 28800, 86400,  // 1h, 2h, 4h, 8h, 1d
			172800, 432000, 864000, 1728000,  // 2d, 5d, 10d, 20d
		),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricGitHubPrCloseDuration, err)
	}

	c.automerged, err = meter.Int64Counter(semconv.MetricGitHubPrAutomerged,
		metric.WithDescription("PRs merged with no APPROVED review (automerge proxy)."),
		metric.WithUnit("{pr}"),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricGitHubPrAutomerged, err)
	}

	c.reviewStatus, err = meter.Int64ObservableGauge(semconv.MetricGitHubPrReviewStatus,
		metric.WithDescription("Open PRs grouped by review decision."),
		metric.WithUnit("{pr}"),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricGitHubPrReviewStatus, err)
	}

	return c, nil
}

// prStats aggregates per-repo PR data for observable instrument callbacks.
type prStats struct {
	// countByStateAndLabel: (state, label) → count for pr.count dimension.
	// Key format: "state\x00label" (NUL separator; neither field contains NUL).
	countByStateAndLabel map[stateLabel]int64
	// reviewStatusCount: reviewStatus → count of open PRs
	reviewStatusCount map[string]int64
	// oldestOpenAge is the age in seconds of the oldest open PR, or 0 if none.
	oldestOpenAge float64
}

type stateLabel struct{ state, label string }

// CollectResult holds the per-repo stats after a Collect call.
type CollectResult struct {
	target string
	org    string
	repo   string
	stats  prStats
}

// PRCollection holds results from one collection cycle.
type PRCollection struct {
	results []CollectResult
}

// Collect fetches PRs for all repos and returns a PRCollection.
// lastCollectedAt is the timestamp of the previous successful collection;
// close.duration and automerged events are only recorded for PRs that closed
// after that time, preventing double-counting across cycles.
// Pass a zero time.Time on the first cycle to skip event recording entirely.
func (c *PRCollector) Collect(ctx context.Context, target string, repos []discovery.Repo, lastCollectedAt time.Time) (*PRCollection, error) {
	lookbackCutoff := time.Now().AddDate(0, 0, -c.cfg.LookbackDays)
	coll := &PRCollection{}

	for _, repo := range repos {
		stats, err := c.collectRepo(ctx, target, repo, lookbackCutoff, lastCollectedAt)
		if err != nil {
			c.logger.Error("PR collection failed for repo",
				"target", target,
				"repo", repo.FullName,
				"err", err,
			)
			continue
		}
		coll.results = append(coll.results, CollectResult{
			target: target,
			org:    repo.Owner,
			repo:   repo.Name,
			stats:  stats,
		})
	}

	return coll, nil
}

// collectRepo fetches PRs for a single repo and computes stats.
func (c *PRCollector) collectRepo(
	ctx context.Context,
	target string,
	repo discovery.Repo,
	lookbackCutoff time.Time,
	lastCollectedAt time.Time,
) (prStats, error) {
	prs, err := c.fetchPRs(ctx, repo)
	if err != nil {
		return prStats{}, err
	}

	stats := prStats{
		countByStateAndLabel: make(map[stateLabel]int64),
		reviewStatusCount:    make(map[string]int64),
	}

	var oldestOpenCreatedAt time.Time
	// recordEvents is false on the first cycle (lastCollectedAt.IsZero()) to
	// avoid replaying the entire lookback window into counters/histograms.
	recordEvents := !lastCollectedAt.IsZero()

	for i := range prs {
		pr := &prs[i]

		labels := make([]string, 0, len(pr.Labels.Nodes))
		for _, l := range pr.Labels.Nodes {
			labels = append(labels, l.Name)
		}

		state := normaliseState(pr.State, pr.IsDraft)

		fp := filter.PR{State: state, Labels: labels}
		if !c.filter.Match(fp) {
			continue
		}

		// ── pr.count: one point per (state, label) pair ───────────────────────
		// Always emit the state dimension (empty label = state-only bucket).
		stats.countByStateAndLabel[stateLabel{state: state, label: ""}]++
		// Also emit one point per label, carrying the state, so consumers can
		// answer "open renovate PRs" and "merged renovate PRs" independently.
		for _, l := range labels {
			stats.countByStateAndLabel[stateLabel{state: state, label: l}]++
		}

		// ── oldest open PR age ────────────────────────────────────────────────
		if state == semconv.AttrGitHubPrStateOpen {
			if oldestOpenCreatedAt.IsZero() || pr.CreatedAt.Before(oldestOpenCreatedAt) {
				oldestOpenCreatedAt = pr.CreatedAt
			}
			rs := reviewDecisionToStatus(pr.ReviewDecision)
			stats.reviewStatusCount[rs]++
		}

		if !recordEvents {
			continue
		}

		// ── close duration histogram — only new events since last cycle ────────
		if state == semconv.AttrGitHubPrStateClosed || state == semconv.AttrGitHubPrStateMerged {
			closedAt := closedAtFor(pr)
			if closedAt != nil && closedAt.After(lastCollectedAt) && closedAt.After(lookbackCutoff) {
				dur := closedAt.Sub(pr.CreatedAt).Seconds()
				attrs := metric.WithAttributes(
					attribute.String(semconv.AttrExporterTarget, target),
					attribute.String(semconv.AttrGitHubOrg, repo.Owner),
					attribute.String(semconv.AttrGitHubRepo, repo.Name),
				)
				c.closeDur.Record(ctx, dur, attrs)
			}
		}

		// ── automerge counter — only new events since last cycle ──────────────
		if state == semconv.AttrGitHubPrStateMerged && pr.MergedAt != nil &&
			pr.MergedAt.After(lastCollectedAt) && pr.MergedAt.After(lookbackCutoff) {
			if !hasApprovedReview(pr.Reviews.Nodes) {
				attrs := metric.WithAttributes(
					attribute.String(semconv.AttrExporterTarget, target),
					attribute.String(semconv.AttrGitHubOrg, repo.Owner),
					attribute.String(semconv.AttrGitHubRepo, repo.Name),
				)
				c.automerged.Add(ctx, 1, attrs)
			}
		}
	}

	if !oldestOpenCreatedAt.IsZero() {
		stats.oldestOpenAge = time.Since(oldestOpenCreatedAt).Seconds()
	}

	return stats, nil
}

// fetchPRs paginates the GraphQL PR query for a repo, stopping at MaxPRsPerRepo.
func (c *PRCollector) fetchPRs(ctx context.Context, repo discovery.Repo) ([]prNode, error) {
	var all []prNode
	var cursor *githubv4.String

	for {
		vars := map[string]interface{}{
			"owner":  githubv4.String(repo.Owner),
			"name":   githubv4.String(repo.Name),
			"cursor": cursor,
		}

		var q prQuery
		if err := c.gql.Query(ctx, &q, vars); err != nil {
			return nil, fmt.Errorf("graphql PR query for %s: %w", repo.FullName, err)
		}

		all = append(all, q.Repository.PullRequests.Nodes...)

		if !q.Repository.PullRequests.PageInfo.HasNextPage {
			break
		}
		if len(all) >= c.cfg.MaxPRsPerRepo {
			c.logger.Warn("max PRs per repo reached; truncating",
				"repo", repo.FullName,
				"limit", c.cfg.MaxPRsPerRepo,
			)
			break
		}

		endCursor := q.Repository.PullRequests.PageInfo.EndCursor
		cursor = &endCursor
	}

	return all, nil
}

// ObservableRegistration returns a metric.Callback for the observable instruments.
func (c *PRCollector) ObservableRegistration(coll *PRCollection) metric.Callback {
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

			// pr.count: emit one point per (state, label) combination.
			// Points with empty label carry only the state dimension.
			for sl, cnt := range r.stats.countByStateAndLabel {
				attrs := make([]attribute.KeyValue, 0, len(base)+2)
				attrs = append(attrs, base...)
				attrs = append(attrs, attribute.String(semconv.AttrGitHubPrState, sl.state))
				if sl.label != "" {
					attrs = append(attrs, attribute.String(semconv.AttrGitHubPrLabel, sl.label))
				}
				obs.ObserveInt64(c.prCount, cnt, metric.WithAttributes(attrs...))
			}

			// pr.age (oldest open)
			if r.stats.oldestOpenAge > 0 {
				obs.ObserveFloat64(c.prAge, r.stats.oldestOpenAge,
					metric.WithAttributes(base...))
			}

			// pr.review_status
			for rs, cnt := range r.stats.reviewStatusCount {
				attrs := append(append([]attribute.KeyValue{}, base...), //nolint:gocritic
					attribute.String(semconv.AttrGitHubPrReviewStatus, rs))
				obs.ObserveInt64(c.reviewStatus, cnt, metric.WithAttributes(attrs...))
			}
		}
		return nil
	}
}

// ObservableInstruments returns the observable instruments for batch RegisterCallback.
func (c *PRCollector) ObservableInstruments() []metric.Observable {
	return []metric.Observable{c.prCount, c.prAge, c.reviewStatus}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// normaliseState converts GitHub GraphQL PR state strings to semconv values.
// Draft PRs have State "OPEN" with IsDraft=true.
func normaliseState(state string, isDraft bool) string {
	switch state {
	case "OPEN":
		if isDraft {
			return semconv.AttrGitHubPrStateDraft
		}
		return semconv.AttrGitHubPrStateOpen
	case "CLOSED":
		return semconv.AttrGitHubPrStateClosed
	case "MERGED":
		return semconv.AttrGitHubPrStateMerged
	default:
		return state
	}
}

// reviewDecisionToStatus maps a nullable GraphQL reviewDecision to a semconv value.
func reviewDecisionToStatus(rd *string) string {
	if rd == nil {
		return semconv.AttrGitHubPrReviewStatusNone
	}
	switch *rd {
	case "APPROVED":
		return semconv.AttrGitHubPrReviewStatusApproved
	case "CHANGES_REQUESTED":
		return semconv.AttrGitHubPrReviewStatusChangesRequested
	case "REVIEW_REQUIRED":
		return semconv.AttrGitHubPrReviewStatusPending
	default:
		return semconv.AttrGitHubPrReviewStatusNone
	}
}

// hasApprovedReview returns true if any review node has state "APPROVED".
func hasApprovedReview(reviews []struct{ State string }) bool {
	for _, r := range reviews {
		if r.State == "APPROVED" {
			return true
		}
	}
	return false
}

// closedAtFor returns the effective close time for a PR.
func closedAtFor(pr *prNode) *time.Time {
	if pr.MergedAt != nil {
		return pr.MergedAt
	}
	return pr.ClosedAt
}
