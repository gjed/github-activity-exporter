//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"go.opentelemetry.io/otel/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/exporter"
	ghclient "github.com/gjed/github-activity-exporter/internal/github"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
	"github.com/gjed/github-activity-exporter/internal/telemetry"
)

// Self-telemetry traces (https://github.com/gjed/github-activity-exporter/issues/75):
// the span live-check, and the metric live-checks with traces on and off.

type staticRepos []discovery.Repo

func (s staticRepos) Repos(context.Context) ([]discovery.Repo, error) { return s, nil }

// tracedCycle returns a live-check self hook that runs self-telemetry with
// metrics and, when traces is set, traces active, inheriting the receiver as
// export.otlp. It runs a collection cycle of all three collectors on
// fixtures (a NOT_FOUND repo in the batched queries) on the product meter,
// and GitHub requests through the transport and client main.go builds
// against a fake GitHub that answers a secondary rate limit, then pauses
// the graphql pool, then a 404.
func tracedCycle(traces bool) func(t *testing.T, addr string, p *exporter.Provider) {
	return func(t *testing.T, addr string, p *exporter.Provider) {
		cfg := config.Telemetry{Enabled: true, Metrics: config.Signal{Enabled: true}, Traces: config.Signal{Enabled: traces}}
		tel := newSelfTelemetrySignals(t, cfg, config.OTLP{Endpoint: addr, Protocol: "grpc", TLS: config.TLS{Insecure: true}}, nil)
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := tel.Shutdown(ctx); err != nil {
				t.Errorf("self-telemetry Shutdown: %v", err)
			}
		})
		if tel.Active(telemetry.SignalTraces) != traces {
			t.Fatalf("traces active = %v, want %v", tel.Active(telemetry.SignalTraces), traces)
		}
		p.AttachProducer(tel.Producer())

		var tracer trace.Tracer
		var tp trace.TracerProvider
		if traces {
			tracer, tp = tel.Tracer(), tel.TracerProvider()
		}
		runFixtureCycle(t, p.Meter(), p.ForceFlush, tracer)
		exerciseRateLimits(t, tel, tp, tracer)

		if traces {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := tel.TracerProvider().(*sdktrace.TracerProvider).ForceFlush(ctx); err != nil {
				t.Fatalf("trace ForceFlush: %v", err)
			}
		}
	}
}

// runFixtureCycle runs one cycle of the three collectors over 6 repos on
// fixtures at concurrency 2, recording on meter and calling flush, if any,
// at its end.
func runFixtureCycle(t *testing.T, meter metric.Meter, flush func(context.Context) error, tracer trace.Tracer) {
	t.Helper()
	var repos staticRepos
	for i := range 6 {
		name := "repo-" + strconv.Itoa(i)
		repos = append(repos, discovery.Repo{Owner: "acme", Name: name, FullName: "acme/" + name})
	}
	limiter := collector.NewLimiter(2)
	issues := &collector.FixtureIssueClient{AliasErrors: map[string]string{"repo-4": "NOT_FOUND"}}
	pr, err := collector.NewPRCollector(&collector.FixturePRClient{}, config.PRFilters{},
		collector.PRCollectorConfig{Limiter: limiter, Tracer: tracer}, meter, nil)
	if err != nil {
		t.Fatal(err)
	}
	iss, err := collector.NewIssueCollector(issues, config.IssueFilters{}, meter, nil,
		collector.WithGraphQLBatchSize(4), collector.WithLimiter(limiter), collector.WithTracer(tracer))
	if err != nil {
		t.Fatal(err)
	}
	dash, err := collector.NewDashboardCollector(issues, "renovate[bot]", meter, nil,
		collector.WithGraphQLBatchSize(4), collector.WithLimiter(limiter), collector.WithTracer(tracer))
	if err != nil {
		t.Fatal(err)
	}
	r, err := collector.NewRunner(config.Target{Name: "acme"}, repos,
		collector.RunnerCollectors{PR: pr, Issue: iss, Dashboard: dash}, meter, nil,
		collector.WithCycleTracer(tracer), collector.WithFlush(flush))
	if err != nil {
		t.Fatal(err)
	}
	r.RunOnce(context.Background())
}

// exerciseRateLimits sends GitHub requests through the instrumented
// transport and client: a GraphQL query answered with a secondary rate limit
// and then with a low graphql pool, a second GraphQL query that waits for the
// pool, a REST listing and a 404.
func exerciseRateLimits(t *testing.T, tel *telemetry.Telemetry, tp trace.TracerProvider, tracer trace.Tracer) {
	t.Helper()
	var graphqlHits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("X-RateLimit-Remaining", "4000")
		h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		switch r.URL.Path {
		case "/graphql":
			h.Set("X-RateLimit-Resource", "graphql")
			switch graphqlHits.Add(1) {
			case 1:
				h.Set("Retry-After", "1")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"You have exceeded a secondary rate limit."}`))
				return
			case 2:
				h.Set("X-RateLimit-Remaining", "150")
				h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(2*time.Second).Unix(), 10))
			}
			_, _ = w.Write([]byte(`{"data":{"viewer":{"login":"octocat"}}}`))
		case "/orgs/acme/repos":
			_, _ = w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		}
	}))
	defer srv.Close()

	rt, err := ghclient.NewTransport(srv.URL, tel.MeterProvider(), tp)
	if err != nil {
		t.Fatalf("NewTransport: %v", err)
	}
	auth := ghclient.NewPATAuthenticatorWithBase("ghp_test", srv.URL, &http.Client{Transport: rt})
	c, err := ghclient.NewClient(auth, ghclient.WithBaseURL(srv.URL), ghclient.WithBaseTransport(rt), ghclient.WithTracer(tracer))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	ctx := context.Background()
	var q struct{ Viewer struct{ Login string } }
	for range 2 {
		if err := c.GraphQL().Query(ctx, &q, nil); err != nil {
			t.Fatalf("GraphQL: %v", err)
		}
	}
	if _, _, err := c.REST().Repositories.ListByOrg(ctx, "acme", nil); err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	_, _, _ = c.REST().Repositories.Get(ctx, "acme", "missing")
}

// liveCheckSpan is a span sample of a live-check JSON report.
type liveCheckSpan struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Attributes []struct {
		Name  string `json:"name"`
		Value any    `json:"value"`
	} `json:"attributes"`
	LiveCheckResult struct {
		MatchInfo struct {
			Signal    *string `json:"signal"`
			Unmatched bool    `json:"unmatched"`
		} `json:"match_info"`
	} `json:"live_check_result"`
}

// liveCheckSpans returns the span samples of a live-check JSON report.
func liveCheckSpans(t *testing.T, report string) []liveCheckSpan {
	t.Helper()
	var spans []liveCheckSpan
	dec := json.NewDecoder(strings.NewReader(report))
	for {
		var doc struct {
			Span *liveCheckSpan `json:"span"`
		}
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return spans
			}
			t.Fatalf("decode live-check report: %v", err)
		}
		if doc.Span != nil {
			spans = append(spans, *doc.Span)
		}
	}
}

// TestLiveCheck_Traces exports a traced cycle and GitHub requests with rate
// limit waits, and asserts live-check finds no violation and pairs every
// span with its registry definition: each exporter span and the HTTP client
// span, with both wait reasons.
func TestLiveCheck_Traces(t *testing.T) {
	out, err := runLiveCheckWithSelfTelemetry(t, emitConformingMetrics, tracedCycle(true))
	if err != nil {
		t.Fatalf("weaver registry live-check failed (schema violation): %v", err)
	}
	signals := map[string]int{}
	reasons := map[any]bool{}
	for _, s := range liveCheckSpans(t, out) {
		mi := s.LiveCheckResult.MatchInfo
		if mi.Signal == nil || mi.Unmatched {
			t.Errorf("span %q (%s) matches no registry span", s.Name, s.Kind)
			continue
		}
		signals[*mi.Signal]++
		for _, a := range s.Attributes {
			if a.Name == sc.AttrGitHubExporterRateLimitReason {
				reasons[a.Value] = true
			}
		}
	}
	for _, want := range []string{
		"http.client", sc.SpanGitHubExporterCycle, sc.SpanGitHubExporterCollect, sc.SpanGitHubExporterRepo,
		sc.SpanGitHubExporterBatch, sc.SpanGitHubExporterFlush, sc.SpanGitHubExporterRateLimitWait,
	} {
		if signals[want] == 0 {
			t.Errorf("live-check saw no %s span (seen: %v)", want, signals)
		}
	}
	if !reasons[sc.AttrGitHubExporterRateLimitReasonPrimary] || !reasons[sc.AttrGitHubExporterRateLimitReasonSecondary] {
		t.Errorf("wait reasons seen: %v, want primary and secondary", reasons)
	}
}

// TestLiveCheck_UndeclaredSpan asserts the span check catches a span no
// matcher pairs with a registry span.
func TestLiveCheck_UndeclaredSpan(t *testing.T) {
	out, _ := runLiveCheckWithSelfTelemetry(t, emitConformingMetrics, func(t *testing.T, addr string, p *exporter.Provider) {
		cfg := config.Telemetry{Enabled: true, Traces: config.Signal{Enabled: true}}
		tel := newSelfTelemetrySignals(t, cfg, config.OTLP{Endpoint: addr, Protocol: "grpc", TLS: config.TLS{Insecure: true}}, nil)
		t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })
		_, span := tel.Tracer().Start(context.Background(), "github_exporter.undeclared")
		span.End()
		if err := tel.TracerProvider().(*sdktrace.TracerProvider).ForceFlush(context.Background()); err != nil {
			t.Fatalf("trace ForceFlush: %v", err)
		}
	})
	spans := liveCheckSpans(t, out)
	if len(spans) != 1 || spans[0].LiveCheckResult.MatchInfo.Signal != nil {
		t.Fatalf("spans = %+v, want the undeclared span with no matched signal", spans)
	}
}

// TestLiveCheck_TracesOnOffSameMetrics runs the same flow with traces active
// and inactive and asserts the OTLP push carries the same metrics, and no
// span with traces inactive.
func TestLiveCheck_TracesOnOffSameMetrics(t *testing.T) {
	seen := map[bool]map[string]int{}
	for _, traces := range []bool{false, true} {
		out, err := runLiveCheckWithSelfTelemetry(t, emitConformingMetrics, tracedCycle(traces))
		if err != nil {
			t.Fatalf("traces=%v: weaver registry live-check failed (schema violation): %v", traces, err)
		}
		stats, err := liveCheckStatistics(out)
		if err != nil {
			t.Fatalf("traces=%v: live-check report: %v", traces, err)
		}
		if len(stats.SeenNonRegistryMetrics) > 0 {
			t.Errorf("traces=%v: non-registry metrics seen: %v", traces, stats.SeenNonRegistryMetrics)
		}
		if n := len(liveCheckSpans(t, out)); !traces && n != 0 {
			t.Errorf("traces inactive: %d spans exported", n)
		}
		seen[traces] = stats.SeenRegistryMetrics
	}
	if !maps.Equal(seen[false], seen[true]) {
		t.Errorf("metrics differ with traces on:\noff: %v\non:  %v", seen[false], seen[true])
	}
}

// scrapeTraced runs the traced flow (the product metrics come from the
// fixture cycle) on an exporter with the Prometheus
// bridge enabled and self-telemetry metrics active, traces on or off, and
// returns the parsed /metrics scrape. OTLP goes nowhere.
func scrapeTraced(t *testing.T, traces bool) map[string]*dto.MetricFamily {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	p, err := exporter.New(context.Background(), exporter.Config{
		OTLPEndpoint:      "http://127.0.0.1:1", // unreachable: the 1h interval never fires
		ExportInterval:    time.Hour,
		PrometheusEnabled: true,
		PrometheusAddr:    addr,
	}, nil)
	if err != nil {
		t.Fatalf("exporter.New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	})

	cfg := config.Telemetry{Enabled: true, Metrics: config.Signal{Enabled: true}, Traces: config.Signal{Enabled: traces}}
	tel := newSelfTelemetrySignals(t, cfg, config.OTLP{Endpoint: "http://127.0.0.1:1", Protocol: "http"}, p.PrometheusRegisterer())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tel.Shutdown(ctx)
	})
	var tracer trace.Tracer
	var tp trace.TracerProvider
	if traces {
		tracer, tp = tel.Tracer(), tel.TracerProvider()
	}
	runFixtureCycle(t, p.Meter(), nil, tracer)
	exerciseRateLimits(t, tel, tp, tracer)

	body := scrape(t, "http://"+addr+"/metrics", scrapeAccept["text"])
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse /metrics: %v\n%s", err, body)
	}
	return families
}

// seriesOf lists every series of families as "name{labels}", with the value
// for github.* series other than ages, which move with the clock, and
// without the labels that change on every run.
func seriesOf(families map[string]*dto.MetricFamily) []string {
	var out []string
	for name, f := range families {
		for _, m := range f.GetMetric() {
			var labels []string
			for _, l := range m.GetLabel() {
				// Random per run: the fake GitHub's port, the process's instance ID.
				if l.GetName() != "server.port" && l.GetName() != "service.instance.id" {
					labels = append(labels, l.GetName()+"="+l.GetValue())
				}
			}
			slices.Sort(labels)
			s := name + "{" + strings.Join(labels, ",") + "}"
			if strings.HasPrefix(name, "github.") && !strings.HasSuffix(name, ".age") {
				switch {
				case m.GetCounter() != nil:
					s += fmt.Sprint(m.GetCounter().GetValue())
				case m.GetGauge() != nil:
					s += fmt.Sprint(m.GetGauge().GetValue())
				}
			}
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

// TestPrometheusLiveCheck_TracesOnOffSameMetrics asserts /metrics carries the
// same series with traces on and off, and passes live-check with traces on.
func TestPrometheusLiveCheck_TracesOnOffSameMetrics(t *testing.T) {
	off := scrapeTraced(t, false)
	on := scrapeTraced(t, true)
	if a, b := seriesOf(off), seriesOf(on); !slices.Equal(a, b) {
		t.Errorf("/metrics differs with traces on:\noff: %v\non:  %v", a, b)
	}
	if _, err := runPrometheusLiveCheck(t, on); err != nil {
		t.Fatalf("weaver registry live-check failed (schema violation): %v", err)
	}
}
