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
	Number   int
	State    string // "OPEN", "CLOSED", "MERGED"
	IsDraft  bool
	CreatedAt time.Time
	MergedAt  *time.Time
	ClosedAt  *time.Time
	Labels   struct {
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

	// OTel instruments
	prCount      metric.Int64ObservableGauge
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

	c.prCount, err = meter.Int64ObservableGauge(semconv.MetricGitHubPrCount,
		metric.WithDescription("Number of pull requests by state."),
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
			3600, 7200, 14400, 28800, 86400,   // 1h, 2h, 4h, 8h, 1d
			172800, 432000, 864000, 1728000,     // 2d, 5d, 10d, 20d
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

// prStats aggregates per-repo PR data for observable gauge callbacks.
type prStats struct {
	// countByState: state → count (e.g., "open" → 12)
	countByState map[string]int64
	// countByLabel: label → count
	countByLabel map[string]int64
	// reviewStatusCount: reviewStatus → count
	reviewStatusCount map[string]int64
	// oldestOpenAge is the age in seconds of the oldest open PR, or 0 if none.
	oldestOpenAge float64
}

// CollectResult holds the per-repo stats after a Collect call.
// The actual metric recording for counters/histograms happens inside Collect.
// The observable gauges are registered separately via RegisterCallbacks.
type CollectResult struct {
	// target and repo labels for attribute sets
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
// It also directly records close.duration and automerged counters (point-in-time events).
func (c *PRCollector) Collect(ctx context.Context, target string, repos []discovery.Repo) (*PRCollection, error) {
	lookbackCutoff := time.Now().AddDate(0, 0, -c.cfg.LookbackDays)
	coll := &PRCollection{}

	for _, repo := range repos {
		stats, err := c.collectRepo(ctx, target, repo, lookbackCutoff)
		if err != nil {
			c.logger.Error("PR collection failed for repo",
				"target", target,
				"repo", repo.FullName,
				"err", err,
			)
			// Collect what we can; don't abort the whole cycle.
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
// It also directly records histogram and counter observations that are events
// (close duration, automerge).
func (c *PRCollector) collectRepo(
	ctx context.Context,
	target string,
	repo discovery.Repo,
	lookbackCutoff time.Time,
) (prStats, error) {
	prs, err := c.fetchPRs(ctx, repo)
	if err != nil {
		return prStats{}, err
	}

	stats := prStats{
		countByState:      make(map[string]int64),
		countByLabel:      make(map[string]int64),
		reviewStatusCount: make(map[string]int64),
	}

	var oldestOpenCreatedAt time.Time

	for i := range prs {
		pr := &prs[i]

		labels := make([]string, 0, len(pr.Labels.Nodes))
		for _, l := range pr.Labels.Nodes {
			labels = append(labels, l.Name)
		}

		// Normalise state to lowercase: "open", "closed", "merged"
		state := normaliseState(pr.State, pr.IsDraft)

		fp := filter.PR{State: state, Labels: labels}
		if !c.filter.Match(fp) {
			continue
		}

		// ── state counts ─────────────────────────────────────────────────────
		stats.countByState[state]++

		// ── label counts ─────────────────────────────────────────────────────
		for _, l := range labels {
			stats.countByLabel[l]++
		}

		// ── oldest open PR age ────────────────────────────────────────────────
		if state == "open" {
			if oldestOpenCreatedAt.IsZero() || pr.CreatedAt.Before(oldestOpenCreatedAt) {
				oldestOpenCreatedAt = pr.CreatedAt
			}

			// ── review status gauge ───────────────────────────────────────────
			rs := reviewDecisionToStatus(pr.ReviewDecision)
			stats.reviewStatusCount[rs]++
		}

		// ── close duration histogram (events within lookback window) ──────────
		if state == "closed" || state == "merged" {
			closedAt := closedAtFor(pr)
			if closedAt != nil && closedAt.After(lookbackCutoff) {
				dur := closedAt.Sub(pr.CreatedAt).Seconds()
				attrs := metric.WithAttributes(
					attribute.String(semconv.AttrExporterTarget, target),
					attribute.String(semconv.AttrGitHubOrg, repo.Owner),
					attribute.String(semconv.AttrGitHubRepo, repo.Name),
				)
				c.closeDur.Record(ctx, dur, attrs)
			}
		}

		// ── automerge counter (merged with no APPROVED review) ────────────────
		if state == "merged" && pr.MergedAt != nil && pr.MergedAt.After(lookbackCutoff) {
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

// ObservableRegistration returns a function suitable for passing to
// meter.RegisterCallback. Call this after Collect so the gauge snapshots
// reflect the latest collection.
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

			// pr.count by state
			for state, cnt := range r.stats.countByState {
				attrs := append(base, attribute.String(semconv.AttrGitHubPrState, state)) //nolint:gocritic
				obs.ObserveInt64(c.prCount, cnt, metric.WithAttributes(attrs...))
			}

			// pr.count by label
			for label, cnt := range r.stats.countByLabel {
				attrs := append(base, attribute.String(semconv.AttrGitHubPrLabel, label)) //nolint:gocritic
				obs.ObserveInt64(c.prCount, cnt, metric.WithAttributes(attrs...))
			}

			// pr.age (oldest open)
			if r.stats.oldestOpenAge > 0 {
				obs.ObserveFloat64(c.prAge, r.stats.oldestOpenAge,
					metric.WithAttributes(base...))
			}

			// pr.review_status
			for rs, cnt := range r.stats.reviewStatusCount {
				attrs := append(base, attribute.String(semconv.AttrGitHubPrReviewStatus, rs)) //nolint:gocritic
				obs.ObserveInt64(c.reviewStatus, cnt, metric.WithAttributes(attrs...))
			}
		}
		return nil
	}
}

// Instruments returns the observable instruments for batch registration.
func (c *PRCollector) Instruments() (metric.Int64ObservableGauge, metric.Float64ObservableGauge, metric.Int64ObservableGauge) {
	return c.prCount, c.prAge, c.reviewStatus
}

// ── helpers ──────────────────────────────────────────────────────────────────

// normaliseState converts GitHub GraphQL state strings to lowercase.
// Draft PRs have state "OPEN" but IsDraft=true — we map them to "draft".
func normaliseState(state string, isDraft bool) string {
	switch state {
	case "OPEN":
		if isDraft {
			return "draft"
		}
		return semconv.AttrGitHubPrStateOpen
	case "CLOSED":
		return semconv.AttrGitHubPrStateClosed
	case "MERGED":
		return "merged"
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

// closedAtFor returns the ClosedAt time for merged or closed PRs.
func closedAtFor(pr *prNode) *time.Time {
	if pr.MergedAt != nil {
		return pr.MergedAt
	}
	return pr.ClosedAt
}
