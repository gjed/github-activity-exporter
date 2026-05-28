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
// It is safe to run concurrently with other Runners (one per target).
type Runner struct {
	target     string
	source     RepoSource
	collectors RunnerCollectors
	meter      metric.Meter
	logger     *slog.Logger

	// latest collections — updated on each cycle, used by observable callbacks
	mu         sync.RWMutex
	latestPR   *PRCollection
	latestIssue *IssueCollection
	latestDash  *DashboardCollection

	// registration handle so we can unregister on shutdown
	registration metric.Registration
}

// NewRunner creates a Runner for the given target.
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
		meter:      meter,
		logger:     logger,
	}

	// Register a single batch callback that delegates to each collector's
	// ObservableRegistration using the latest collected data.
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
// It delegates to each collector's registration using the latest collected data.
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

// Run executes the collection loop: fetch repos, run all collectors, update
// latest snapshots. It blocks until ctx is cancelled.
// Call this in a goroutine (one per target).
func (r *Runner) Run(ctx context.Context) {
	r.logger.Info("runner started", "target", r.target)
	defer r.logger.Info("runner stopped", "target", r.target)

	for {
		select {
		case <-ctx.Done():
			// Unregister observable callbacks on shutdown.
			if r.registration != nil {
				if err := r.registration.Unregister(); err != nil {
					r.logger.Error("unregister callback", "target", r.target, "err", err)
				}
			}
			return
		default:
			r.runOnce(ctx)
			// After a single collection, wait for ctx cancellation.
			// The OTel PeriodicReader drives timing; we just need to collect once
			// per Collect() invocation from the SDK. But to support standalone
			// invocation (e.g., integration tests), we also accept a direct Run().
			// The outer ticker loop in main.go controls the interval.
			select {
			case <-ctx.Done():
				if r.registration != nil {
					if err := r.registration.Unregister(); err != nil {
						r.logger.Error("unregister callback", "target", r.target, "err", err)
					}
				}
				return
			case <-time.After(runnerCollectInterval):
				// Re-collect on next tick
			}
		}
	}
}

// runnerCollectInterval is how often Run() re-collects when not driven by an
// external ticker. Defaults to 5 minutes; overridden in tests via RunOnce.
const runnerCollectInterval = 5 * time.Minute

// RunOnce performs a single collection cycle (exposed for testing).
func (r *Runner) RunOnce(ctx context.Context) {
	r.runOnce(ctx)
}

// runOnce performs a single collection cycle for this target.
func (r *Runner) runOnce(ctx context.Context) {
	start := time.Now()
	r.logger.Debug("collection cycle started", "target", r.target)

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
		prColl, err = r.collectors.PR.Collect(ctx, r.target, repos)
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
	r.mu.Unlock()

	r.logger.Debug("collection cycle complete",
		"target", r.target,
		"repos", len(repos),
		"duration", time.Since(start),
	)
}
