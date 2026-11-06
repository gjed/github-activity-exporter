package collector_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/semconv"
)

type staticRepoSource struct{ repos []discovery.Repo }

func (s staticRepoSource) Repos(context.Context) ([]discovery.Repo, error) { return s.repos, nil }

type fakeSelfMetrics struct {
	scrapes    []string
	apiErrors  []string // endpoint per recorded error
	otlpErrors []string // target per recorded error
}

func (f *fakeSelfMetrics) RecordScrapeDuration(_ context.Context, target string, _ time.Time) {
	f.scrapes = append(f.scrapes, target)
}

func (f *fakeSelfMetrics) RecordAPIError(_ context.Context, _, endpoint string) {
	f.apiErrors = append(f.apiErrors, endpoint)
}

func (f *fakeSelfMetrics) RecordOTLPError(_ context.Context, target string) {
	f.otlpErrors = append(f.otlpErrors, target)
}

// flakyPRClient returns nodes, or an error while fail is set.
type flakyPRClient struct {
	fail  bool
	nodes []collector.ExportedPRNode
}

func (f *flakyPRClient) Query(_ context.Context, q interface{}, _ map[string]interface{}) error {
	if f.fail {
		return errors.New("graphql: 502 bad gateway")
	}
	return collector.InjectPRQueryResult(q, f.nodes, false)
}

func TestRunner_RecordsScrapeDurationPerCycle(t *testing.T) {
	self := &fakeSelfMetrics{}
	meter := metric.NewMeterProvider().Meter("test")

	r, err := collector.NewRunner(config.Target{Name: "tgt"}, staticRepoSource{},
		collector.RunnerCollectors{}, meter, nil, collector.WithSelfMetrics(self))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	r.RunOnce(context.Background())
	r.RunOnce(context.Background())

	if len(self.scrapes) != 2 || self.scrapes[0] != "tgt" {
		t.Fatalf("scrape recordings = %v, want 2 for target tgt", self.scrapes)
	}
}

// A PR merged while its repo's collection is failing must still be counted
// once the repo recovers: a failed repo must not advance lastCollectedAt.
func TestRunner_FailedRepoKeepsEventWindow(t *testing.T) {
	ctx := context.Background()
	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")
	self := &fakeSelfMetrics{}
	gql := &flakyPRClient{}

	prColl, err := collector.NewPRCollector(gql, config.PRFilters{},
		collector.PRCollectorConfig{MaxPRsPerRepo: 500, LookbackDays: 30}, meter, nil)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}
	repos := []discovery.Repo{{Owner: "org", Name: "repo", FullName: "org/repo"}}
	r, err := collector.NewRunner(config.Target{Name: "tgt"}, staticRepoSource{repos: repos},
		collector.RunnerCollectors{PR: prColl}, meter, nil, collector.WithSelfMetrics(self))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	r.RunOnce(ctx) // cycle 1: no PRs, establishes the window

	time.Sleep(5 * time.Millisecond)
	mergedAt := time.Now()
	time.Sleep(5 * time.Millisecond)

	gql.fail = true
	r.RunOnce(ctx) // cycle 2: repo fails

	gql.fail = false
	gql.nodes = []collector.ExportedPRNode{{
		Number: 1, State: "MERGED", CreatedAt: mergedAt.Add(-time.Hour), MergedAt: &mergedAt,
	}}
	r.RunOnce(ctx) // cycle 3: repo recovers, PR merged before cycle 2 started

	rm := collectMetrics(t, reader)
	var automerged int64
	for _, dp := range findGaugeInt(t, rm, semconv.MetricGitHubPrAutomerged) {
		automerged += dp.Value
	}
	if automerged != 1 {
		t.Errorf("github.pr.automerged = %d, want 1", automerged)
	}
	if len(self.apiErrors) != 1 || self.apiErrors[0] != semconv.AttrGitHubApiEndpointGraphQL {
		t.Errorf("api errors = %v, want [%s]", self.apiErrors, semconv.AttrGitHubApiEndpointGraphQL)
	}
}

func TestRunner_FlushesOncePerCycle(t *testing.T) {
	flushes := 0
	meter := metric.NewMeterProvider().Meter("test")

	r, err := collector.NewRunner(config.Target{Name: "tgt"}, staticRepoSource{},
		collector.RunnerCollectors{}, meter, nil,
		collector.WithFlush(func(context.Context) error { flushes++; return nil }))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	r.RunOnce(context.Background())

	if flushes != 1 {
		t.Fatalf("flushes = %d, want 1", flushes)
	}
}

func TestRunner_SkipsFlushWhenContextCancelled(t *testing.T) {
	flushes := 0
	meter := metric.NewMeterProvider().Meter("test")

	r, err := collector.NewRunner(config.Target{Name: "tgt"}, staticRepoSource{},
		collector.RunnerCollectors{}, meter, nil,
		collector.WithFlush(func(context.Context) error { flushes++; return nil }))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.RunOnce(ctx)

	if flushes != 0 {
		t.Fatalf("flushes = %d, want 0", flushes)
	}
}

func TestRunner_FlushErrorIsLoggedCountedAndNonFatal(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	self := &fakeSelfMetrics{}
	flushed := make(chan struct{}, 100)
	meter := metric.NewMeterProvider().Meter("test")

	r, err := collector.NewRunner(config.Target{Name: "tgt"}, staticRepoSource{},
		collector.RunnerCollectors{}, meter, logger,
		collector.WithInterval(time.Millisecond),
		collector.WithSelfMetrics(self),
		collector.WithFlush(func(context.Context) error {
			select {
			case flushed <- struct{}{}:
			default:
			}
			return errors.New("context deadline exceeded")
		}))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()

	// A second flush proves the loop survived the first failed one. Wait for
	// a third: cycles are sequential, so its start guarantees the first two
	// errors were recorded before cancel (an error returned after cancel is
	// dropped as shutdown).
	for i := 0; i < 3; i++ {
		select {
		case <-flushed:
		case <-done:
			t.Fatal("Run returned after a flush error")
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for flush %d", i+1)
		}
	}
	cancel()
	<-done

	if len(self.otlpErrors) < 2 || self.otlpErrors[0] != "tgt" {
		t.Fatalf("otlp errors = %v, want at least 2 for target tgt", self.otlpErrors)
	}
	if !strings.Contains(logs.String(), "metrics flush failed") {
		t.Fatalf("flush error not logged; logs:\n%s", logs.String())
	}
}

type failingRepoSource struct{}

func (failingRepoSource) Repos(context.Context) ([]discovery.Repo, error) {
	return nil, errors.New("discovery: 401 bad credentials")
}

// recordingObserver records cycle events as "start:<target>" and
// "finish:<target>:<discoveryOK>".
type recordingObserver struct{ events []string }

func (o *recordingObserver) CycleStarted(target string) {
	o.events = append(o.events, "start:"+target)
}

func (o *recordingObserver) CycleFinished(target string, discoveryOK bool) {
	o.events = append(o.events, fmt.Sprintf("finish:%s:%t", target, discoveryOK))
}

func TestRunner_ReportsCycleToObserver(t *testing.T) {
	tests := []struct {
		name   string
		source collector.RepoSource
		want   string
	}{
		{"discovery succeeds", staticRepoSource{}, "[start:tgt finish:tgt:true]"},
		{"discovery fails", failingRepoSource{}, "[start:tgt finish:tgt:false]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obs := &recordingObserver{}
			meter := metric.NewMeterProvider().Meter("test")
			r, err := collector.NewRunner(config.Target{Name: "tgt"}, tt.source,
				collector.RunnerCollectors{}, meter, nil, collector.WithCycleObserver(obs))
			if err != nil {
				t.Fatalf("NewRunner: %v", err)
			}

			r.RunOnce(context.Background())

			if got := fmt.Sprint(obs.events); got != tt.want {
				t.Errorf("observer events = %s, want %s", got, tt.want)
			}
		})
	}
}
