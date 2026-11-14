package collector_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shurcooL/githubv4"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	ghclient "github.com/gjed/github-activity-exporter/internal/github"
)

// inFlight tracks the GitHub requests in flight across the fake clients of
// one target, and the peak.
type inFlight struct{ now, peak atomic.Int64 }

func (f *inFlight) enter() {
	n := f.now.Add(1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	// Long enough for other workers to overlap.
	time.Sleep(200 * time.Microsecond)
}

func (f *inFlight) leave() { f.now.Add(-1) }

// perRepoPRClient serves each repo its own FixturePRClient and fails the
// repos in failing.
type perRepoPRClient struct {
	track   *inFlight
	repos   map[string]*collector.FixturePRClient
	failing map[string]bool
}

func (c *perRepoPRClient) Query(ctx context.Context, q interface{}, vars map[string]interface{}) error {
	c.track.enter()
	defer c.track.leave()
	name := string(vars["name"].(githubv4.String))
	if c.failing[name] {
		return errors.New("graphql: 502 bad gateway")
	}
	return c.repos[name].Query(ctx, q, vars)
}

// trackedIssueClient counts the issue and dashboard requests in flight.
type trackedIssueClient struct {
	*collector.FixtureIssueClient
	track *inFlight
}

func (c trackedIssueClient) Query(ctx context.Context, q interface{}, vars map[string]interface{}) error {
	c.track.enter()
	defer c.track.leave()
	return c.FixtureIssueClient.Query(ctx, q, vars)
}

func (c trackedIssueClient) QueryRaw(ctx context.Context, query string, vars map[string]any) (*ghclient.RawResponse, error) {
	c.track.enter()
	defer c.track.leave()
	return c.FixtureIssueClient.QueryRaw(ctx, query, vars)
}

// concurrencyRun is what a sequence of cycles exported at one concurrency.
type concurrencyRun struct {
	series    []string
	apiErrors []string
	peak      int64
}

// runCycles runs four cycles of all three collectors over 30 repos with the
// given concurrency: open PRs, then events with a failing repo, then the
// repo's recovery and a removed repo, then nothing new. Issue and dashboard
// batches include a failed alias and resource-limit fallbacks. A non-nil
// tracer traces the cycles.
func runCycles(t *testing.T, concurrency int, tracer trace.Tracer) concurrencyRun {
	t.Helper()
	ctx := context.Background()
	mp, reader := newTestMeterProvider()
	meter := mp.Meter("test")
	self := &fakeSelfMetrics{}
	track := &inFlight{}
	limiter := collector.NewLimiter(concurrency)

	repos := batchRepos(30)
	pr := &perRepoPRClient{track: track, repos: map[string]*collector.FixturePRClient{}, failing: map[string]bool{}}
	start := time.Now()
	for i, repo := range repos {
		var nodes []collector.ExportedPRNode
		for n := range i % 4 {
			nodes = append(nodes, collector.ExportedPRNode{
				Number: n + 1, State: "OPEN", CreatedAt: start.Add(-time.Duration(n+1) * time.Hour),
				Author: "renovate", AuthorIsBot: true, Labels: []string{"deps"},
			})
		}
		pr.repos[repo.Name] = &collector.FixturePRClient{Nodes: nodes, PageSize: 2}
	}

	issues := batchFixture()
	issues.AliasErrors = map[string]string{
		repos[4].Name:  "NOT_FOUND",
		repos[7].Name:  "RESOURCE_LIMITS_EXCEEDED",
		repos[21].Name: "RESOURCE_LIMITS_EXCEEDED",
	}
	gql := trackedIssueClient{FixtureIssueClient: issues, track: track}

	prColl, err := collector.NewPRCollector(pr, config.PRFilters{},
		collector.PRCollectorConfig{LookbackDays: 30, Limiter: limiter, Tracer: tracer}, meter, nil)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}
	issColl, err := collector.NewIssueCollector(gql, config.IssueFilters{}, meter, nil,
		collector.WithGraphQLBatchSize(4), collector.WithLimiter(limiter), collector.WithTracer(tracer))
	if err != nil {
		t.Fatalf("NewIssueCollector: %v", err)
	}
	dashColl, err := collector.NewDashboardCollector(gql, "renovate[bot]", meter, nil,
		collector.WithGraphQLBatchSize(4), collector.WithLimiter(limiter), collector.WithTracer(tracer))
	if err != nil {
		t.Fatalf("NewDashboardCollector: %v", err)
	}
	src := &mutableRepoSource{repos: repos}
	r, err := collector.NewRunner(config.Target{Name: "tgt"}, src,
		collector.RunnerCollectors{PR: prColl, Issue: issColl, Dashboard: dashColl},
		meter, nil, collector.WithSelfMetrics(self), collector.WithCycleTracer(tracer),
		collector.WithFlush(func(context.Context) error { return nil }))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	r.RunOnce(ctx) // cycle 1: open PRs, establishes the windows

	time.Sleep(5 * time.Millisecond)
	at := time.Now()
	time.Sleep(5 * time.Millisecond)
	for i, repo := range repos {
		f := pr.repos[repo.Name]
		f.Nodes = append(f.Nodes,
			collector.ExportedPRNode{Number: 100, State: "MERGED", CreatedAt: at.Add(-90 * time.Minute), MergedAt: &at, Author: "renovate", AuthorIsBot: true},
			collector.ExportedPRNode{Number: 101, State: "CLOSED", CreatedAt: at.Add(-time.Duration(i) * time.Hour), ClosedAt: &at},
		)
	}
	pr.failing[repos[3].Name] = true
	r.RunOnce(ctx) // cycle 2: events; repo 3 fails

	delete(pr.failing, repos[3].Name)
	src.repos = repos[:29]
	r.RunOnce(ctx) // cycle 3: repo 3 recovers and counts its events; repo 29 removed
	r.RunOnce(ctx) // cycle 4: nothing new

	return concurrencyRun{
		series:    exportedSeries(t, collectMetrics(t, reader)),
		apiErrors: self.apiErrors,
		peak:      track.peak.Load(),
	}
}

type mutableRepoSource struct{ repos []discovery.Repo }

func (s *mutableRepoSource) Repos(context.Context) ([]discovery.Repo, error) { return s.repos, nil }

// exportedSeries lists every data point as "<metric>{attrs}=<value>",
// sorted. Float gauge values (ages) move with the clock and are left out;
// histograms are listed by count and bucket counts.
func exportedSeries(t *testing.T, rm metricdata.ResourceMetrics) []string {
	t.Helper()
	var out []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, dp := range d.DataPoints {
					out = append(out, fmt.Sprintf("%s{%s}=%d", m.Name, attrString(dp.Attributes), dp.Value))
				}
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					out = append(out, fmt.Sprintf("%s{%s}=%d", m.Name, attrString(dp.Attributes), dp.Value))
				}
			case metricdata.Gauge[float64]:
				for _, dp := range d.DataPoints {
					out = append(out, fmt.Sprintf("%s{%s}", m.Name, attrString(dp.Attributes)))
				}
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					out = append(out, fmt.Sprintf("%s{%s}=%d%v", m.Name, attrString(dp.Attributes), dp.Count, dp.BucketCounts))
				}
			default:
				t.Fatalf("unexpected data type %T for %s", m.Data, m.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// The same cycles export the same series at concurrency 1 and 8, and the
// number of requests in flight never exceeds the concurrency.
func TestRunner_SerialAndConcurrentExportTheSame(t *testing.T) {
	serial := runCycles(t, 1, nil)
	concurrent := runCycles(t, 8, nil)

	if serial.peak != 1 {
		t.Errorf("concurrency 1: peak requests in flight = %d, want 1", serial.peak)
	}
	if concurrent.peak < 2 || concurrent.peak > 8 {
		t.Errorf("concurrency 8: peak requests in flight = %d, want 2 to 8", concurrent.peak)
	}
	if len(serial.series) == 0 {
		t.Fatal("no series exported")
	}
	if !slices.Equal(serial.series, concurrent.series) {
		t.Errorf("series differ:\nserial:     %v\nconcurrent: %v", serial.series, concurrent.series)
	}
	if !slices.Equal(serial.apiErrors, concurrent.apiErrors) {
		t.Errorf("api.errors differ: serial %v, concurrent %v", serial.apiErrors, concurrent.apiErrors)
	}
	// Repo 4 fails the issue and dashboard queries in every cycle; repo 3
	// fails the PR scan once.
	if got := len(serial.apiErrors); got != 9 {
		t.Errorf("api.errors increments = %d (%v), want 9", got, serial.apiErrors)
	}
}

// One Limiter shared by the PR, issue and dashboard collectors bounds the
// requests in flight across them.
func TestRunner_SharedLimiterAcrossCollectors(t *testing.T) {
	for _, n := range []int{2, 4} {
		if got := runCycles(t, n, nil).peak; got > int64(n) || got < 2 {
			t.Errorf("concurrency %d: peak requests in flight = %d", n, got)
		}
	}
}
