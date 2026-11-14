package collector_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	ghclient "github.com/gjed/github-activity-exporter/internal/github"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
)

// logRecord is a record as captureHandler saw it.
type logRecord struct {
	msg   string
	span  trace.SpanContext
	attrs map[string]string
}

// captureHandler records every record with the span context it was logged with.
type captureHandler struct {
	mu      *sync.Mutex
	records *[]logRecord
}

func newCaptureLogger() (*slog.Logger, func() []logRecord) {
	h := captureHandler{mu: &sync.Mutex{}, records: &[]logRecord{}}
	return slog.New(h), func() []logRecord {
		h.mu.Lock()
		defer h.mu.Unlock()
		return append([]logRecord(nil), *h.records...)
	}
}

func (captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h captureHandler) Handle(ctx context.Context, r slog.Record) error {
	rec := logRecord{msg: r.Message, span: trace.SpanContextFromContext(ctx), attrs: map[string]string{}}
	r.Attrs(func(a slog.Attr) bool {
		rec.attrs[a.Key] = a.Value.String()
		return true
	})
	h.mu.Lock()
	*h.records = append(*h.records, rec)
	h.mu.Unlock()
	return nil
}

func (h captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h captureHandler) WithGroup(string) slog.Handler      { return h }

// Per-unit failures are logged after the workers, in repo order, with the
// context of the unit's span: the failed repo's span for the PR collector,
// the failed batch's span for the issue and dashboard collectors. Keys are
// registry attribute names.
func TestLog_FailuresCarryTheirUnitSpan(t *testing.T) {
	tp, rec := newRecorder(t)
	logger, records := newCaptureLogger()
	runCyclesLogged(t, 4, tp.Tracer("test"), logger)

	spans := map[trace.SpanID]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		spans[s.SpanContext().SpanID()] = s
	}
	want := map[string]string{
		"PR collection failed for repo":    sc.SpanGitHubExporterRepo,
		"issue collection failed for repo": sc.SpanGitHubExporterBatch,
		"dashboard collection failed":      sc.SpanGitHubExporterBatch,
	}
	seen := map[string]int{}
	for _, r := range records() {
		spanName, ok := want[r.msg]
		if !ok {
			continue
		}
		seen[r.msg]++
		s, ok := spans[r.span.SpanID()]
		if !ok {
			t.Errorf("%q logged outside a recorded span", r.msg)
			continue
		}
		if s.Name() != spanName || s.Status().Code != codes.Error {
			t.Errorf("%q logged in span %s (status %v), want a failed %s", r.msg, s.Name(), s.Status().Code, spanName)
		}
		if spanName == sc.SpanGitHubExporterRepo && attrsOf(s)[sc.AttrGitHubRepo].AsString() != r.attrs[sc.AttrGitHubRepo] {
			t.Errorf("%q for repo %s logged in the span of repo %s", r.msg, r.attrs[sc.AttrGitHubRepo], attrsOf(s)[sc.AttrGitHubRepo].AsString())
		}
		if r.attrs[sc.AttrExporterTarget] != "tgt" || r.attrs[sc.AttrGitHubOrg] != "org" || r.attrs[sc.AttrGitHubRepo] == "" {
			t.Errorf("%q attributes %v, want exporter.target, github.org, github.repo", r.msg, r.attrs)
		}
		for _, old := range []string{"target", "repo"} {
			if _, ok := r.attrs[old]; ok {
				t.Errorf("%q has the old key %q", r.msg, old)
			}
		}
	}
	// Repo 3 fails once in the PR collector; repo 4 in every issue and
	// dashboard batch of the four cycles.
	if seen["PR collection failed for repo"] != 1 || seen["issue collection failed for repo"] != 4 || seen["dashboard collection failed"] != 4 {
		t.Errorf("failure records = %v", seen)
	}
}

// A rate-limit record logged by the GitHub client while a Limiter worker
// collects a repo carries the span of that repo unit.
func TestLog_RateLimitInWorkerCarriesRepoSpan(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("X-RateLimit-Resource", "graphql")
		h.Set("X-RateLimit-Remaining", "4000")
		h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		if hits.Add(1) == 1 {
			h.Set("X-RateLimit-Remaining", "10")
			h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	logger, records := newCaptureLogger()
	auth := ghclient.NewPATAuthenticatorWithBase("ghp_test", srv.URL, srv.Client())
	gh, err := ghclient.NewClient(auth, ghclient.WithBaseURL(srv.URL), ghclient.WithLogger(logger))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tp, rec := newRecorder(t)
	mp, _ := newTestMeterProvider()
	prColl, err := collector.NewPRCollector(gh.GraphQL(), config.PRFilters{},
		collector.PRCollectorConfig{LookbackDays: 30, Limiter: collector.NewLimiter(2), Tracer: tp.Tracer("test")}, mp.Meter("t"), logger)
	if err != nil {
		t.Fatalf("NewPRCollector: %v", err)
	}
	repos := []discovery.Repo{{Owner: "org", Name: "a", FullName: "org/a"}, {Owner: "org", Name: "b", FullName: "org/b"}}
	ctx, parent := tp.Tracer("test").Start(context.Background(), "collect")
	if _, err := prColl.Collect(ctx, "tgt", repos, map[string]time.Time{}, time.Now()); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	parent.End()

	repoSpans := map[trace.SpanID]bool{}
	for _, s := range rec.Ended() {
		if s.Name() == sc.SpanGitHubExporterRepo && s.Parent().SpanID() == parent.SpanContext().SpanID() {
			repoSpans[s.SpanContext().SpanID()] = true
		}
	}
	var found bool
	for _, r := range records() {
		if r.msg != "rate limit threshold reached, pausing requests" {
			continue
		}
		found = true
		if r.span.TraceID() != parent.SpanContext().TraceID() || !repoSpans[r.span.SpanID()] {
			t.Errorf("rate-limit record in span %s, want a repo span of trace %s", r.span.SpanID(), parent.SpanContext().TraceID())
		}
		if r.attrs[sc.AttrGitHubApiRateLimitResource] != "graphql" {
			t.Errorf("attributes %v, want github.api.rate_limit.resource=graphql", r.attrs)
		}
	}
	if !found {
		t.Fatal("no rate-limit record")
	}
}
