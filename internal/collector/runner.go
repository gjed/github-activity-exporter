package collector

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/semconv"
)

// RepoSource provides the current list of repositories for a target.
// Satisfied by *discovery.Discoverer.
type RepoSource interface {
	Repos(ctx context.Context) ([]discovery.Repo, error)
}

// RunnerCollectors bundles the collectors driven by the Runner.
type RunnerCollectors struct {
	PR        *PRCollector
	Issue     *IssueCollector
	Dashboard *DashboardCollector
}

// Runner drives all collectors for one target on each collection cycle.
// It owns the collection interval timer and per-target state. It is safe
// to run concurrently with other Runners (one per target).
type Runner struct {
	target     string
	source     RepoSource
	collectors RunnerCollectors
	interval   time.Duration
	logger     *slog.Logger

	// mu guards all mutable state below.
	mu sync.RWMutex

	// latest collections — updated on each cycle, read by the OTel callback.
	latestPR    *PRCollection
	latestIssue *IssueCollection
	latestDash  *DashboardCollection

	// lastCollectedAt maps repo FullName to the start of the last cycle in
	// which that repo's PRs were collected successfully. Used to gate
	// event-based instruments (close.duration, automerged) so each event is
	// recorded once. A repo whose collection fails keeps its previous value,
	// so its events are picked up on the next successful cycle. In-memory
	// only: events that happen while the exporter is down are not recorded.
	lastCollectedAt map[string]time.Time

	// registration handle so we can unregister on shutdown.
	registration metric.Registration

	// self records exporter self-metrics; nil disables them.
	self SelfMetricsRecorder

	// flush pushes the collected metrics at the end of each cycle; nil
	// disables it.
	flush func(context.Context) error
}

// SelfMetricsRecorder records exporter self-metrics for a collection cycle.
// Satisfied by *exporter.SelfMetrics.
type SelfMetricsRecorder interface {
	RecordScrapeDuration(ctx context.Context, target string, start time.Time)
	RecordAPIError(ctx context.Context, target, endpoint string)
	RecordOTLPError(ctx context.Context, target string)
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithInterval sets the collection interval. Non-positive values keep the
// default (5 minutes).
func WithInterval(d time.Duration) RunnerOption {
	return func(r *Runner) {
		if d > 0 {
			r.interval = d
		}
	}
}

// WithSelfMetrics records github_exporter.scrape.duration once per cycle and
// github_exporter.api.errors once per failed GitHub API collection (repo
// discovery, or one collector on one repo).
func WithSelfMetrics(sm SelfMetricsRecorder) RunnerOption {
	return func(r *Runner) { r.self = sm }
}

// WithFlush calls flush once at the end of every collection cycle so the
// cycle's metrics are pushed immediately (e.g. Provider.ForceFlush). A flush
// error is logged and counted in github_exporter.otlp.errors; it never stops
// the loop.
func WithFlush(flush func(context.Context) error) RunnerOption {
	return func(r *Runner) { r.flush = flush }
}

// NewRunner creates a Runner for the given target. The collection interval
// defaults to 5 minutes; override it with WithInterval.
func NewRunner(
	target config.Target,
	source RepoSource,
	colls RunnerCollectors,
	meter metric.Meter,
	logger *slog.Logger,
	opts ...RunnerOption,
) (*Runner, error) {
	if logger == nil {
		logger = slog.Default()
	}

	r := &Runner{
		target:     target.Name,
		source:     source,
		collectors: colls,
		interval:   5 * time.Minute,
		logger:     logger,

		lastCollectedAt: make(map[string]time.Time),
	}
	for _, opt := range opts {
		opt(r)
	}

	// Register one batch callback that serves all observable instruments using
	// the latest collected snapshots. The OTel SDK calls this on every metric
	// collection (per-cycle flush, PeriodicReader tick, Prometheus scrape); we
	// just return whatever the most recent cycle gave us.
	instruments := make([]metric.Observable, 0)
	if colls.PR != nil {
		instruments = append(instruments, colls.PR.ObservableInstruments()...)
	}
	if colls.Issue != nil {
		instruments = append(instruments, colls.Issue.ObservableInstruments()...)
	}
	if colls.Dashboard != nil {
		instruments = append(instruments, colls.Dashboard.ObservableInstruments()...)
	}

	reg, err := meter.RegisterCallback(r.observableCallback, instruments...)
	if err != nil {
		return nil, err
	}
	r.registration = reg

	return r, nil
}

// observableCallback is called by the OTel SDK on each metric collection cycle.
func (r *Runner) observableCallback(ctx context.Context, obs metric.Observer) error {
	r.mu.RLock()
	pr := r.latestPR
	issue := r.latestIssue
	dash := r.latestDash
	r.mu.RUnlock()

	if r.collectors.PR != nil && pr != nil {
		if err := r.collectors.PR.ObservableRegistration(pr)(ctx, obs); err != nil {
			return err
		}
	}
	if r.collectors.Issue != nil && issue != nil {
		if err := r.collectors.Issue.ObservableRegistration(issue)(ctx, obs); err != nil {
			return err
		}
	}
	if r.collectors.Dashboard != nil && dash != nil {
		if err := r.collectors.Dashboard.ObservableRegistration(dash)(ctx, obs); err != nil {
			return err
		}
	}
	return nil
}

// Run executes the collection loop, sleeping between cycles for the configured
// interval. It blocks until ctx is cancelled, then unregisters callbacks and
// returns — allowing the caller to wait for graceful shutdown via WaitGroup.
func (r *Runner) Run(ctx context.Context) {
	r.logger.Info("runner started", "target", r.target, "interval", r.interval)
	defer func() {
		if r.registration != nil {
			if err := r.registration.Unregister(); err != nil {
				r.logger.Error("unregister callback", "target", r.target, "err", err)
			}
		}
		r.logger.Info("runner stopped", "target", r.target)
	}()

	for {
		r.runOnce(ctx)

		select {
		case <-ctx.Done():
			return
		case <-time.After(r.interval):
		}
	}
}

// RunOnce performs a single collection cycle. Exposed for testing.
func (r *Runner) RunOnce(ctx context.Context) {
	r.runOnce(ctx)
}

// runOnce performs one collection cycle, then flushes its metrics.
func (r *Runner) runOnce(ctx context.Context) {
	r.collect(ctx)
	r.flushMetrics(ctx)
}

// flushMetrics pushes the collected metrics. Skipped once ctx is cancelled:
// shutdown performs its own final flush.
func (r *Runner) flushMetrics(ctx context.Context) {
	if r.flush == nil || ctx.Err() != nil {
		return
	}
	err := r.flush(ctx)
	switch {
	case err == nil || ctx.Err() != nil:
		// Shutdown landed mid-flush; it performs its own final flush.
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		// Timed out, e.g. queued behind another target's slow export. Export
		// failures are already counted in otlp.errors by the OTLP exporter.
		r.logger.Warn("metrics flush timed out", "target", r.target, "err", err)
	default:
		r.logger.Warn("metrics flush failed", "target", r.target, "err", err)
		if r.self != nil {
			r.self.RecordOTLPError(ctx, r.target)
		}
	}
}

// collect discovers repos, runs all collectors, updates snapshots and
// advances lastCollectedAt.
func (r *Runner) collect(ctx context.Context) {
	start := time.Now()
	r.logger.Debug("collection cycle started", "target", r.target)

	r.mu.RLock()
	lastCollectedAt := make(map[string]time.Time, len(r.lastCollectedAt))
	for k, v := range r.lastCollectedAt {
		lastCollectedAt[k] = v
	}
	r.mu.RUnlock()

	repos, err := r.source.Repos(ctx)
	if err != nil {
		r.logger.Error("repo discovery failed", "target", r.target, "err", err)
		r.recordAPIErrors(ctx, 1, semconv.AttrGitHubApiEndpointRestRepos)
		return
	}

	var (
		prColl   *PRCollection
		issColl  *IssueCollection
		dashColl *DashboardCollection
	)

	if r.collectors.PR != nil {
		prColl, err = r.collectors.PR.Collect(ctx, r.target, repos, lastCollectedAt)
		if err != nil {
			r.logger.Error("PR collection failed", "target", r.target, "err", err)
		}
	}

	if r.collectors.Issue != nil {
		issColl, err = r.collectors.Issue.Collect(ctx, r.target, repos)
		if err != nil {
			r.logger.Error("issue collection failed", "target", r.target, "err", err)
		}
	}

	if r.collectors.Dashboard != nil {
		dashColl, err = r.collectors.Dashboard.Collect(ctx, r.target, repos)
		if err != nil {
			r.logger.Error("dashboard collection failed", "target", r.target, "err", err)
		}
	}

	// Advance lastCollectedAt only for repos whose PR collection succeeded;
	// failed repos keep their previous timestamp. Repos no longer discovered
	// are dropped.
	next := make(map[string]time.Time, len(repos))
	if prColl != nil {
		failed := make(map[string]bool, len(prColl.FailedRepos()))
		for _, name := range prColl.FailedRepos() {
			failed[name] = true
		}
		for _, repo := range repos {
			if failed[repo.FullName] {
				if t, ok := lastCollectedAt[repo.FullName]; ok {
					next[repo.FullName] = t
				}
				continue
			}
			next[repo.FullName] = start
		}
	}

	r.mu.Lock()
	r.latestPR = prColl
	r.latestIssue = issColl
	r.latestDash = dashColl
	r.lastCollectedAt = next
	r.mu.Unlock()

	if prColl != nil {
		r.recordAPIErrors(ctx, len(prColl.FailedRepos()), semconv.AttrGitHubApiEndpointGraphQL)
	}
	if issColl != nil {
		r.recordAPIErrors(ctx, len(issColl.FailedRepos()), semconv.AttrGitHubApiEndpointRestIssues)
	}
	if dashColl != nil {
		r.recordAPIErrors(ctx, len(dashColl.FailedRepos()), semconv.AttrGitHubApiEndpointRestIssues)
	}
	if r.self != nil {
		r.self.RecordScrapeDuration(ctx, r.target, start)
	}

	r.logger.Debug("collection cycle complete",
		"target", r.target,
		"repos", len(repos),
		"duration", time.Since(start),
	)
}

// recordAPIErrors adds n to github_exporter.api.errors for this target and
// GitHub API endpoint.
func (r *Runner) recordAPIErrors(ctx context.Context, n int, endpoint string) {
	if r.self == nil {
		return
	}
	for i := 0; i < n; i++ {
		r.self.RecordAPIError(ctx, r.target, endpoint)
	}
}
