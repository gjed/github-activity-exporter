package collector

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/renovate-exporter/internal/config"
	"github.com/gjed/renovate-exporter/internal/discovery"
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

	// lastCollectedAt is the wall-clock time the previous successful collection
	// cycle completed. Zero on first run. Used to gate event-based instruments
	// (close.duration, automerged) so we only record truly new events per cycle.
	lastCollectedAt time.Time

	// registration handle so we can unregister on shutdown.
	registration metric.Registration
}

// NewRunner creates a Runner for the given target.
// interval controls how often the collection loop fires; pass 0 to use the
// default (5 minutes).
func NewRunner(
	target config.Target,
	source RepoSource,
	colls RunnerCollectors,
	meter metric.Meter,
	logger *slog.Logger,
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
	}

	// Register one batch callback that serves all observable instruments using
	// the latest collected snapshots. The OTel SDK calls this on each
	// PeriodicReader flush; we just return whatever the most recent cycle gave us.
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

// runOnce performs one collection cycle: discover repos, run all collectors,
// update snapshots and advance lastCollectedAt.
func (r *Runner) runOnce(ctx context.Context) {
	start := time.Now()
	r.logger.Debug("collection cycle started", "target", r.target)

	r.mu.RLock()
	lastCollectedAt := r.lastCollectedAt
	r.mu.RUnlock()

	repos, err := r.source.Repos(ctx)
	if err != nil {
		r.logger.Error("repo discovery failed", "target", r.target, "err", err)
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

	r.mu.Lock()
	r.latestPR = prColl
	r.latestIssue = issColl
	r.latestDash = dashColl
	r.lastCollectedAt = start
	r.mu.Unlock()

	r.logger.Debug("collection cycle complete",
		"target", r.target,
		"repos", len(repos),
		"duration", time.Since(start),
	)
}
