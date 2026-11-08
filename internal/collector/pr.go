// Package collector implements the GitHub data collectors that fetch PR and
// issue state and record OTel metrics.
package collector

import (
	"context"
	"fmt"
	"log/slog"
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

const defaultLookbackDays = 30

// GraphQLClient is the minimal interface used by PRCollector (satisfied by
// *githubv4.Client).
type GraphQLClient interface {
	Query(ctx context.Context, q interface{}, variables map[string]interface{}) error
}

// prNode is the GraphQL response shape for a single PR node.
type prNode struct {
	Number      int
	State       string // "OPEN", "CLOSED", "MERGED"
	IsDraft     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	MergedAt    *time.Time
	ClosedAt    *time.Time
	HeadRefName string
	// Author is nil when the author account was deleted.
	Author *struct {
		Typename string `graphql:"__typename"`
		Login    string
	}
	Labels struct {
		Nodes []struct{ Name string }
	} `graphql:"labels(first: 20)"`
	Reviews struct {
		Nodes []struct{ State string }
	} `graphql:"reviews(first: 50)"`
	ReviewDecision *string
}

// prQuery is the paginated GraphQL v4 query for pull requests. $states and
// $labels are nullable: nil means no server-side filter.
type prQuery struct {
	Repository struct {
		PullRequests struct {
			Nodes    []prNode
			PageInfo struct {
				HasNextPage bool
				EndCursor   githubv4.String
			}
		} `graphql:"pullRequests(first: 100, states: $states, labels: $labels, orderBy: $orderBy, after: $cursor)"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

// PRCollectorConfig holds configurable limits for the PR collector.
type PRCollectorConfig struct {
	// LookbackDays caps how far back the event window reaches. Defaults to 30.
	LookbackDays int
}

// PRCollector fetches PRs via GraphQL and records OTel metrics.
type PRCollector struct {
	gql    GraphQLClient
	filter *filter.PRFilter
	cfg    PRCollectorConfig
	logger *slog.Logger

	// windows holds each repo's event scan state, keyed by FullName. Only
	// the Runner goroutine of the collector's target calls Collect.
	windows map[string]*eventWindow

	// Server-side filter arguments derived from the PR filters; nil means
	// no server-side filter.
	labels      *[]githubv4.String
	eventStates *[]githubv4.PullRequestState
	// skipOpen is true when the states filter excludes both open and draft.
	skipOpen bool

	// OTel instruments.
	// pr.count and issue.count are updowncounters in the registry; the
	// observable variant lets us report a full snapshot each cycle without
	// needing to track deltas manually.
	prCount      metric.Int64ObservableUpDownCounter
	prAge        metric.Float64ObservableGauge
	closeDur     metric.Float64Histogram
	automerged   metric.Int64Counter
	created      metric.Int64Counter
	merged       metric.Int64Counter
	closed       metric.Int64Counter
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

		windows: make(map[string]*eventWindow),
	}
	c.labels, c.eventStates, c.skipOpen = serverSideFilters(filterCfg)

	var err error

	c.prCount, err = meter.Int64ObservableUpDownCounter(semconv.MetricGitHubPrCount,
		metric.WithDescription("Number of open pull requests grouped by state and label."),
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
			3600, 7200, 14400, 28800, 86400, // 1h, 2h, 4h, 8h, 1d
			172800, 432000, 864000, 1728000, // 2d, 5d, 10d, 20d
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

	c.created, err = meter.Int64Counter(semconv.MetricGitHubPrCreated,
		metric.WithDescription("Number of pull requests created."),
		metric.WithUnit("{pr}"),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricGitHubPrCreated, err)
	}

	c.merged, err = meter.Int64Counter(semconv.MetricGitHubPrMerged,
		metric.WithDescription("Number of pull requests merged."),
		metric.WithUnit("{pr}"),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricGitHubPrMerged, err)
	}

	c.closed, err = meter.Int64Counter(semconv.MetricGitHubPrClosed,
		metric.WithDescription("Number of pull requests closed without being merged."),
		metric.WithUnit("{pr}"),
	)
	if err != nil {
		return nil, fmt.Errorf("register %s: %w", semconv.MetricGitHubPrClosed, err)
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

// serverSideFilters maps the PR filters GraphQL can apply to query
// arguments: include_labels to $labels (any-of, as the client-side rule) and
// states to the event scan's $states. The client-side filter re-checks every
// rule, so these only reduce traffic.
func serverSideFilters(cfg config.PRFilters) (labels *[]githubv4.String, states *[]githubv4.PullRequestState, skipOpen bool) {
	if len(cfg.IncludeLabels) > 0 {
		l := make([]githubv4.String, 0, len(cfg.IncludeLabels))
		for _, name := range cfg.IncludeLabels {
			l = append(l, githubv4.String(name))
		}
		labels = &l
	}
	if len(cfg.States) == 0 {
		return labels, nil, false
	}
	seen := make(map[githubv4.PullRequestState]bool)
	var st []githubv4.PullRequestState
	for _, s := range cfg.States {
		var gs githubv4.PullRequestState
		switch strings.ToLower(s) {
		case semconv.AttrGitHubPrStateOpen, semconv.AttrGitHubPrStateDraft:
			gs = githubv4.PullRequestStateOpen
		case semconv.AttrGitHubPrStateClosed:
			gs = githubv4.PullRequestStateClosed
		case semconv.AttrGitHubPrStateMerged:
			gs = githubv4.PullRequestStateMerged
		default:
			continue
		}
		if !seen[gs] {
			seen[gs] = true
			st = append(st, gs)
		}
	}
	if len(st) > 0 {
		states = &st
	}
	return labels, states, !seen[githubv4.PullRequestStateOpen] && len(st) > 0
}

// prStats aggregates per-repo PR data for observable instrument callbacks.
type prStats struct {
	// countByStateAndLabel: (state, label) → count for pr.count dimension.
	countByStateAndLabel map[stateLabel]int64
	// reviewStatusCount: reviewStatus → count of open PRs
	reviewStatusCount map[string]int64
	// oldestOpenAge is the age in seconds of the oldest open PR, or 0 if none.
	oldestOpenAge float64
}

type stateLabel struct{ state, label string }

// prEvents holds the PR events of one repo in one event window. They are
// recorded only once both scans of the repo succeed.
type prEvents struct {
	created, merged, closed, automerged int64
	closeDurations                      []float64
	// counted maps each event counted in this scan to its time.
	counted map[eventKey]time.Time
}

type eventKind uint8

const (
	eventCreated eventKind = iota
	eventMerged
	eventClosed
)

// eventKey identifies one PR event across cycles.
type eventKey struct {
	number int
	kind   eventKind
}

// eventWindow is a repo's event scan state after its last successful cycle.
type eventWindow struct {
	// scanFrom is the start of that cycle's window: the next scan starts
	// there, one cycle before its own window, to catch PRs the previous scan
	// skipped because they were updated while it paged.
	scanFrom time.Time
	// counted holds the events already counted that the next scan can see
	// again, with their time.
	counted map[eventKey]time.Time
}

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

	// failed lists the FullName of repos whose collection failed this cycle.
	failed []string
}

// FailedRepos returns the FullName of every repo whose collection failed.
func (c *PRCollection) FailedRepos() []string { return c.failed }

// Collect fetches PRs for all repos and returns a PRCollection.
//
// until is the end of this cycle's event window (the cycle start, which the
// caller stores as the repo's next lastCollectedAt). lastCollectedAt maps
// repo FullName to the end of that repo's previous window. A repo's events
// are those in (max(until − lookback, lastCollectedAt), until]. The event
// scan reaches one window further back and skips events already counted, so
// a PR the previous scan missed because it was updated mid-scan is counted
// now, and no event is counted twice. Repos missing from the map (first
// cycle, newly discovered) record no events; their counters are created at
// zero so that rate()/increase() see the first event.
func (c *PRCollector) Collect(ctx context.Context, target string, repos []discovery.Repo, lastCollectedAt map[string]time.Time, until time.Time) (*PRCollection, error) {
	lookbackCutoff := until.AddDate(0, 0, -c.cfg.LookbackDays)
	coll := &PRCollection{}
	current := make(map[string]bool, len(repos))

	for _, repo := range repos {
		current[repo.FullName] = true
		since, ok := lastCollectedAt[repo.FullName]
		scanFrom := since
		var counted map[eventKey]time.Time
		if w := c.windows[repo.FullName]; w != nil {
			counted = w.counted
			if w.scanFrom.Before(scanFrom) {
				scanFrom = w.scanFrom
			}
		}
		if scanFrom.Before(lookbackCutoff) {
			scanFrom = lookbackCutoff
		}

		stats, events, err := c.collectRepo(ctx, repo, ok, scanFrom, until, counted)
		if err != nil {
			c.logger.Error("PR collection failed for repo",
				"target", target,
				"repo", repo.FullName,
				"err", err,
			)
			coll.failed = append(coll.failed, repo.FullName)
			continue
		}
		c.recordEvents(ctx, target, repo, events)
		if ok {
			c.windows[repo.FullName] = nextWindow(since, counted, events.counted)
		}
		coll.results = append(coll.results, CollectResult{
			target: target,
			org:    repo.Owner,
			repo:   repo.Name,
			stats:  stats,
		})
	}

	for name := range c.windows {
		if !current[name] {
			delete(c.windows, name)
		}
	}
	return coll, nil
}

// nextWindow keeps the counted events the next scan, which starts at since,
// can see again.
func nextWindow(since time.Time, sets ...map[eventKey]time.Time) *eventWindow {
	w := &eventWindow{scanFrom: since, counted: make(map[eventKey]time.Time)}
	for _, set := range sets {
		for k, t := range set {
			if t.After(since) {
				w.counted[k] = t
			}
		}
	}
	return w
}

// collectRepo runs the open scan and, when the repo has a previous window,
// the event scan for (scanFrom, until].
func (c *PRCollector) collectRepo(
	ctx context.Context,
	repo discovery.Repo,
	scanEvents bool,
	scanFrom, until time.Time,
	counted map[eventKey]time.Time,
) (prStats, prEvents, error) {
	stats, err := c.scanOpen(ctx, repo)
	if err != nil {
		return prStats{}, prEvents{}, err
	}
	if !scanEvents {
		return stats, prEvents{}, nil
	}
	events, err := c.scanEvents(ctx, repo, scanFrom, until, counted)
	if err != nil {
		return prStats{}, prEvents{}, err
	}
	return stats, events, nil
}

// scanOpen reads every open PR (drafts included) with no cap and computes
// the open-state gauges.
func (c *PRCollector) scanOpen(ctx context.Context, repo discovery.Repo) (prStats, error) {
	stats := prStats{
		countByStateAndLabel: make(map[stateLabel]int64),
		reviewStatusCount:    make(map[string]int64),
	}
	if c.skipOpen {
		return stats, nil
	}

	var oldestOpenCreatedAt time.Time
	states := []githubv4.PullRequestState{githubv4.PullRequestStateOpen}
	order := githubv4.IssueOrder{Field: githubv4.IssueOrderFieldCreatedAt, Direction: githubv4.OrderDirectionAsc}

	err := c.paginate(ctx, repo, &states, order, func(pr *prNode) bool {
		state, labels, ok := c.match(pr)
		if !ok {
			return true
		}

		// pr.count: one point per (state, label) pair, plus the state-only
		// bucket (empty label).
		stats.countByStateAndLabel[stateLabel{state: state}]++
		for _, l := range labels {
			stats.countByStateAndLabel[stateLabel{state: state, label: l}]++
		}

		if state == semconv.AttrGitHubPrStateOpen {
			if oldestOpenCreatedAt.IsZero() || pr.CreatedAt.Before(oldestOpenCreatedAt) {
				oldestOpenCreatedAt = pr.CreatedAt
			}
			stats.reviewStatusCount[reviewDecisionToStatus(pr.ReviewDecision)]++
		}
		return true
	})
	if err != nil {
		return prStats{}, err
	}

	if !oldestOpenCreatedAt.IsZero() {
		stats.oldestOpenAge = time.Since(oldestOpenCreatedAt).Seconds()
	}
	return stats, nil
}

// scanEvents reads PRs by last update, newest first, and stops after the
// first page whose last PR was updated at or before since: every PR with an
// event in (since, until] was updated after since and sits before that point.
// Events in counted were recorded by an earlier cycle and are skipped.
//
// A PR updated while the scan pages jumps to the front, behind the cursor,
// and is missed; the next scan starts at this one's since and counts it.
func (c *PRCollector) scanEvents(ctx context.Context, repo discovery.Repo, since, until time.Time, counted map[eventKey]time.Time) (prEvents, error) {
	ev := prEvents{counted: make(map[eventKey]time.Time)}
	// count reports whether the event at t is in the window and not yet
	// counted, and marks it counted.
	count := func(number int, kind eventKind, t *time.Time) bool {
		if t == nil || !t.After(since) || t.After(until) {
			return false
		}
		k := eventKey{number: number, kind: kind}
		if _, done := counted[k]; done {
			return false
		}
		if _, done := ev.counted[k]; done {
			return false
		}
		ev.counted[k] = *t
		return true
	}
	order := githubv4.IssueOrder{Field: githubv4.IssueOrderFieldUpdatedAt, Direction: githubv4.OrderDirectionDesc}

	err := c.paginate(ctx, repo, c.eventStates, order, func(pr *prNode) bool {
		cont := pr.UpdatedAt.After(since)
		state, _, ok := c.match(pr)
		if !ok {
			return cont
		}

		if count(pr.Number, eventCreated, &pr.CreatedAt) {
			ev.created++
		}
		switch {
		case state == semconv.AttrGitHubPrStateMerged && count(pr.Number, eventMerged, pr.MergedAt):
			ev.merged++
			ev.closeDurations = append(ev.closeDurations, pr.MergedAt.Sub(pr.CreatedAt).Seconds())
			if !hasApprovedReview(pr.Reviews.Nodes) {
				ev.automerged++
			}
		case state == semconv.AttrGitHubPrStateClosed && count(pr.Number, eventClosed, pr.ClosedAt):
			ev.closed++
			ev.closeDurations = append(ev.closeDurations, pr.ClosedAt.Sub(pr.CreatedAt).Seconds())
		}
		return cont
	})
	if err != nil {
		return prEvents{}, err
	}
	return ev, nil
}

// paginate runs the PR query with the given states and order, calling visit
// for each node. It stops after the last page, or after a page in which
// visit returned false for the last node.
func (c *PRCollector) paginate(
	ctx context.Context,
	repo discovery.Repo,
	states *[]githubv4.PullRequestState,
	order githubv4.IssueOrder,
	visit func(*prNode) bool,
) error {
	var cursor *githubv4.String
	for {
		vars := map[string]interface{}{
			"owner":   githubv4.String(repo.Owner),
			"name":    githubv4.String(repo.Name),
			"states":  states,
			"labels":  c.labels,
			"orderBy": order,
			"cursor":  cursor,
		}

		var q prQuery
		if err := c.gql.Query(ctx, &q, vars); err != nil {
			return fmt.Errorf("graphql PR query for %s: %w", repo.FullName, err)
		}

		cont := true
		nodes := q.Repository.PullRequests.Nodes
		for i := range nodes {
			cont = visit(&nodes[i])
		}

		if !q.Repository.PullRequests.PageInfo.HasNextPage || !cont {
			return nil
		}
		endCursor := q.Repository.PullRequests.PageInfo.EndCursor
		cursor = &endCursor
	}
}

// match applies the PR filters and returns the normalised state and labels.
func (c *PRCollector) match(pr *prNode) (state string, labels []string, ok bool) {
	labels = make([]string, 0, len(pr.Labels.Nodes))
	for _, l := range pr.Labels.Nodes {
		labels = append(labels, l.Name)
	}
	state = normaliseState(pr.State, pr.IsDraft)
	ok = c.filter.Match(filter.PR{
		State:   state,
		Labels:  labels,
		Author:  authorLogin(pr),
		HeadRef: pr.HeadRefName,
	})
	return state, labels, ok
}

// recordEvents adds a repo's window events to the counters and histogram.
// The counters are always added to, even with zero, so their series exist
// from a repo's first cycle.
func (c *PRCollector) recordEvents(ctx context.Context, target string, repo discovery.Repo, ev prEvents) {
	attrs := metric.WithAttributes(
		attribute.String(semconv.AttrExporterTarget, target),
		attribute.String(semconv.AttrGitHubOrg, repo.Owner),
		attribute.String(semconv.AttrGitHubRepo, repo.Name),
	)
	c.created.Add(ctx, ev.created, attrs)
	c.merged.Add(ctx, ev.merged, attrs)
	c.closed.Add(ctx, ev.closed, attrs)
	if ev.automerged > 0 {
		c.automerged.Add(ctx, ev.automerged, attrs)
	}
	for _, d := range ev.closeDurations {
		c.closeDur.Record(ctx, d, attrs)
	}
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

// authorLogin returns the PR author's REST login. GraphQL returns a GitHub
// App bot's login without the "[bot]" suffix that REST and config use.
func authorLogin(pr *prNode) string {
	if pr.Author == nil {
		return ""
	}
	return restLogin(pr.Author.Typename, pr.Author.Login)
}

// restLogin converts a GraphQL author login to its REST form: GraphQL reports
// a GitHub App bot as "renovate" with __typename Bot, REST as "renovate[bot]".
func restLogin(typename, login string) string {
	if typename == "Bot" && !strings.HasSuffix(login, "[bot]") {
		return login + "[bot]"
	}
	return login
}
