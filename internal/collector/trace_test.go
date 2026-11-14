package collector_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shurcooL/githubv4"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/exporter"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
)

func newRecorder(t *testing.T) (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return tp, rec
}

func attrsOf(s sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	m := map[attribute.Key]attribute.Value{}
	for _, kv := range s.Attributes() {
		m[kv.Key] = kv.Value
	}
	return m
}

// The four cycles of runCycles at concurrency 4 give four traces. In each,
// the cycle is the only root, its collector and flush spans are its
// children, and every repo and batch span, though run in a worker
// goroutine, is a child of its collector span.
func TestTrace_CycleSpanTree(t *testing.T) {
	tp, rec := newRecorder(t)
	runCycles(t, 4, tp.Tracer("test"))
	spans := rec.Ended()

	byID := map[trace.SpanID]sdktrace.ReadOnlySpan{}
	children := map[trace.SpanID][]sdktrace.ReadOnlySpan{}
	var roots []sdktrace.ReadOnlySpan
	for _, s := range spans {
		byID[s.SpanContext().SpanID()] = s
		if len(s.Events()) > 0 {
			t.Errorf("span %s has events %v; errors are not recorded as span events", s.Name(), s.Events())
		}
		if !s.Parent().IsValid() {
			roots = append(roots, s)
			continue
		}
		children[s.Parent().SpanID()] = append(children[s.Parent().SpanID()], s)
	}
	if len(roots) != 4 {
		t.Fatalf("root spans = %d, want 4 (one per cycle)", len(roots))
	}
	for _, s := range spans {
		if s.Parent().IsValid() {
			if _, ok := byID[s.Parent().SpanID()]; !ok {
				t.Errorf("span %s has a parent outside the recorded spans", s.Name())
			}
		}
	}

	outcomes := []string{}
	var repoCounts []int64
	for cycle, root := range roots {
		if root.Name() != sc.SpanGitHubExporterCycle {
			t.Fatalf("root span %q, want %q", root.Name(), sc.SpanGitHubExporterCycle)
		}
		a := attrsOf(root)
		if a[sc.AttrExporterTarget].AsString() != "tgt" {
			t.Errorf("cycle %d exporter.target = %q", cycle, a[sc.AttrExporterTarget].AsString())
		}
		outcomes = append(outcomes, a[sc.AttrGitHubExporterCycleOutcome].AsString())
		repoCounts = append(repoCounts, a[sc.AttrGitHubExporterCycleRepos].AsInt64())

		var collectors []string
		var flushes int
		for _, c := range children[root.SpanContext().SpanID()] {
			if c.SpanContext().TraceID() != root.SpanContext().TraceID() {
				t.Errorf("cycle %d: child %s in another trace", cycle, c.Name())
			}
			switch c.Name() {
			case sc.SpanGitHubExporterFlush:
				flushes++
				if n := len(children[c.SpanContext().SpanID()]); n != 0 {
					t.Errorf("cycle %d: flush span has %d children", cycle, n)
				}
			case sc.SpanGitHubExporterCollect:
				kind := attrsOf(c)[sc.AttrGitHubExporterCollector].AsString()
				collectors = append(collectors, kind)
				units := children[c.SpanContext().SpanID()]
				want := sc.SpanGitHubExporterBatch
				repos := int(a[sc.AttrGitHubExporterCycleRepos].AsInt64())
				wantN := (repos + 3) / 4 // batches of 4
				if kind == sc.AttrGitHubExporterCollectorPr {
					want, wantN = sc.SpanGitHubExporterRepo, repos
				}
				failed := 0
				for _, u := range units {
					if u.Name() != want {
						t.Errorf("cycle %d %s: unit span %q, want %q", cycle, kind, u.Name(), want)
					}
					failed += int(attrsOf(u)[sc.AttrGitHubExporterFailedRepos].AsInt64())
					if u.Status().Code == codes.Error && kind == sc.AttrGitHubExporterCollectorPr {
						failed++
					}
				}
				if len(units) != wantN {
					t.Errorf("cycle %d %s: %d unit spans, want %d", cycle, kind, len(units), wantN)
				}
				if got := int(attrsOf(c)[sc.AttrGitHubExporterFailedRepos].AsInt64()); got != failed {
					t.Errorf("cycle %d %s: failed_repos %d, units say %d", cycle, kind, got, failed)
				}
			default:
				t.Errorf("cycle %d: unexpected child %q of the cycle span", cycle, c.Name())
			}
		}
		slices.Sort(collectors)
		if !slices.Equal(collectors, []string{"dashboard", "issue", "pr"}) || flushes != 1 {
			t.Errorf("cycle %d: collectors %v, flushes %d", cycle, collectors, flushes)
		}
	}
	slices.Sort(repoCounts)
	if !slices.Equal(repoCounts, []int64{29, 29, 30, 30}) {
		t.Errorf("cycle repo counts = %v, want two of 30 then two of 29", repoCounts)
	}
	// Repo 4 is NOT_FOUND in every batched query: every cycle is partial.
	if !slices.Equal(outcomes, []string{"partial", "partial", "partial", "partial"}) {
		t.Errorf("outcomes = %v", outcomes)
	}
}

// A failed repo marks its unit and collector spans with status Error and a
// bounded error.type: the GraphQL type of a batched query, _OTHER for an
// untyped error.
func TestTrace_FailedUnits(t *testing.T) {
	tp, rec := newRecorder(t)
	runCycles(t, 4, tp.Tracer("test"))

	var notFoundBatches, failedRepoSpans int
	for _, s := range rec.Ended() {
		a := attrsOf(s)
		switch s.Name() {
		case sc.SpanGitHubExporterBatch:
			if a[sc.AttrGitHubExporterFailedRepos].AsInt64() == 1 {
				if s.Status().Code != codes.Error || a["error.type"].AsString() != "NOT_FOUND" {
					t.Errorf("failed batch: status %v error.type %q", s.Status(), a["error.type"].AsString())
				}
				notFoundBatches++
			} else if s.Status().Code == codes.Error {
				t.Errorf("batch without failures has status %v", s.Status())
			}
		case sc.SpanGitHubExporterRepo:
			if s.Status().Code == codes.Error {
				failedRepoSpans++
				if a["error.type"].AsString() != "_OTHER" || a[sc.AttrGitHubRepo].AsString() == "" || a[sc.AttrGitHubOrg].AsString() == "" {
					t.Errorf("failed repo span attributes %v", s.Attributes())
				}
			}
		}
	}
	// 4 cycles x (issue + dashboard) batch holding repo 4; repo 3 fails once.
	if notFoundBatches != 8 || failedRepoSpans != 1 {
		t.Errorf("NOT_FOUND batches = %d (want 8), failed repo spans = %d (want 1)", notFoundBatches, failedRepoSpans)
	}
}

// Tracing does not change what the cycles export.
func TestTrace_MetricsIndependentOfTraces(t *testing.T) {
	tp, _ := newRecorder(t)
	off := runCycles(t, 4, nil)
	on := runCycles(t, 4, tp.Tracer("test"))
	if !slices.Equal(off.series, on.series) || !slices.Equal(off.apiErrors, on.apiErrors) {
		t.Errorf("exported metrics differ with traces on:\noff: %v\non:  %v", off.series, on.series)
	}
}

type failingSource struct{}

func (failingSource) Repos(context.Context) ([]discovery.Repo, error) {
	return nil, errors.New("discovery: 502 bad gateway")
}

// A failed discovery is a failed cycle with no collector spans.
func TestTrace_DiscoveryFailure(t *testing.T) {
	tp, rec := newRecorder(t)
	mp, _ := newTestMeterProvider()
	pr, err := collector.NewPRCollector(&collector.FixturePRClient{}, config.PRFilters{}, collector.PRCollectorConfig{}, mp.Meter("t"), nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := collector.NewRunner(config.Target{Name: "tgt"}, failingSource{}, collector.RunnerCollectors{PR: pr},
		mp.Meter("t"), nil, collector.WithCycleTracer(tp.Tracer("test")))
	if err != nil {
		t.Fatal(err)
	}
	r.RunOnce(context.Background())
	spans := rec.Ended()
	if len(spans) != 1 || spans[0].Name() != sc.SpanGitHubExporterCycle {
		t.Fatalf("spans = %d, want only the cycle span", len(spans))
	}
	a := attrsOf(spans[0])
	if spans[0].Status().Code != codes.Error || a[sc.AttrGitHubExporterCycleOutcome].AsString() != "failure" || a["error.type"].AsString() != "_OTHER" {
		t.Errorf("cycle: status %v attributes %v", spans[0].Status(), spans[0].Attributes())
	}
	if _, ok := a[sc.AttrGitHubExporterCycleRepos]; ok {
		t.Error("failed cycle has github_exporter.cycle.repos")
	}
}

// The per-cycle flush pushes metrics over OTLP with no span other than
// github_exporter.flush, even with the recording provider installed globally.
func TestTrace_FlushIsNotTraced(t *testing.T) {
	var pushes atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pushes.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	tp, rec := newRecorder(t)
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	p, err := exporter.New(context.Background(), exporter.Config{
		OTLPEndpoint:   srv.URL,
		Protocol:       exporter.ProtocolHTTP,
		ExportInterval: time.Hour,
	}, nil)
	if err != nil {
		t.Fatalf("exporter.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	pr, err := collector.NewPRCollector(&collector.FixturePRClient{}, config.PRFilters{},
		collector.PRCollectorConfig{Tracer: tp.Tracer("test")}, p.Meter(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := collector.NewRunner(config.Target{Name: "tgt"}, &mutableRepoSource{repos: batchRepos(1)},
		collector.RunnerCollectors{PR: pr}, p.Meter(), nil,
		collector.WithCycleTracer(tp.Tracer("test")), collector.WithFlush(p.ForceFlush))
	if err != nil {
		t.Fatal(err)
	}
	r.RunOnce(context.Background())
	if pushes.Load() == 0 {
		t.Fatal("the flush pushed nothing")
	}
	var names []string
	for _, s := range rec.Ended() {
		names = append(names, s.Name())
	}
	slices.Sort(names)
	want := []string{sc.SpanGitHubExporterCollect, sc.SpanGitHubExporterCycle, sc.SpanGitHubExporterFlush, sc.SpanGitHubExporterRepo}
	if !slices.Equal(names, want) {
		t.Errorf("spans = %v, want %v", names, want)
	}
}

// prRouter serves each repo its own FixturePRClient, without delay.
type prRouter map[string]*collector.FixturePRClient

func (p prRouter) Query(ctx context.Context, q interface{}, vars map[string]interface{}) error {
	return p[string(vars["name"].(githubv4.String))].Query(ctx, q, vars)
}

// benchRunner builds a Runner for 30 repos with all three collectors on
// fixtures, at concurrency 4, traced by tracer (nil: off).
func benchRunner(b *testing.B, tracer trace.Tracer) *collector.Runner {
	b.Helper()
	mp, _ := newTestMeterProvider()
	meter := mp.Meter("bench")
	limiter := collector.NewLimiter(4)
	repos := batchRepos(30)
	pr := prRouter{}
	now := time.Now()
	for i, repo := range repos {
		var nodes []collector.ExportedPRNode
		for n := range i % 4 {
			nodes = append(nodes, collector.ExportedPRNode{
				Number: n + 1, State: "OPEN", CreatedAt: now.Add(-time.Hour),
				Author: "renovate", AuthorIsBot: true, Labels: []string{"deps"},
			})
		}
		pr[repo.Name] = &collector.FixturePRClient{Nodes: nodes, PageSize: 2}
	}
	gql := batchFixture()
	prColl, err := collector.NewPRCollector(pr, config.PRFilters{},
		collector.PRCollectorConfig{LookbackDays: 30, Limiter: limiter, Tracer: tracer}, meter, nil)
	if err != nil {
		b.Fatal(err)
	}
	issColl, err := collector.NewIssueCollector(gql, config.IssueFilters{}, meter, nil,
		collector.WithGraphQLBatchSize(4), collector.WithLimiter(limiter), collector.WithTracer(tracer))
	if err != nil {
		b.Fatal(err)
	}
	dashColl, err := collector.NewDashboardCollector(gql, "renovate[bot]", meter, nil,
		collector.WithGraphQLBatchSize(4), collector.WithLimiter(limiter), collector.WithTracer(tracer))
	if err != nil {
		b.Fatal(err)
	}
	r, err := collector.NewRunner(config.Target{Name: "tgt"}, &mutableRepoSource{repos: repos},
		collector.RunnerCollectors{PR: prColl, Issue: issColl, Dashboard: dashColl}, meter, nil,
		collector.WithCycleTracer(tracer), collector.WithFlush(func(context.Context) error { return nil }))
	if err != nil {
		b.Fatal(err)
	}
	return r
}

// BenchmarkCycle runs one collection cycle on fixtures with traces off and
// on (batch span processor, discarding exporter).
func BenchmarkCycle(b *testing.B) {
	b.Run("traces=off", func(b *testing.B) {
		r := benchRunner(b, nil)
		b.ReportAllocs()
		for b.Loop() {
			r.RunOnce(context.Background())
		}
	})
	b.Run("traces=on", func(b *testing.B) {
		tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(tracetest.NewNoopExporter()))
		b.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
		r := benchRunner(b, tp.Tracer("bench"))
		b.ReportAllocs()
		for b.Loop() {
			r.RunOnce(context.Background())
		}
	})
}

// A flush that shutdown interrupts is not a failed span: shutdown performs
// its own final flush.
func TestTrace_FlushInterruptedByShutdown(t *testing.T) {
	tp, rec := newRecorder(t)
	mp, _ := newTestMeterProvider()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := collector.NewRunner(config.Target{Name: "tgt"}, &mutableRepoSource{repos: batchRepos(1)},
		collector.RunnerCollectors{}, mp.Meter("t"), nil, collector.WithCycleTracer(tp.Tracer("test")),
		collector.WithFlush(func(context.Context) error {
			cancel()
			return context.Canceled
		}))
	if err != nil {
		t.Fatal(err)
	}
	r.RunOnce(ctx)
	for _, s := range rec.Ended() {
		if s.Name() == sc.SpanGitHubExporterFlush && s.Status().Code == codes.Error {
			t.Errorf("flush span status %v, want unset", s.Status())
		}
	}
}
