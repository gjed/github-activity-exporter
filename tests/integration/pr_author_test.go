//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/github-activity-exporter/internal/collector"
	"github.com/gjed/github-activity-exporter/internal/config"
	"github.com/gjed/github-activity-exporter/internal/discovery"
	"github.com/gjed/github-activity-exporter/internal/exporter"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
)

// prAuthorHistograms are the metrics that carry github.pr.author when the
// target sets collection.pr_author.
var prAuthorHistograms = []string{sc.MetricGitHubPrCloseDuration, sc.MetricGitHubPrLinesAdded, sc.MetricGitHubPrLinesRemoved}

// runPRSizeCycles runs two PR collection cycles on fixtures: the first opens
// the repo's event window, the second records a user PR, a bot PR and a PR
// without author, merged or closed in it.
func runPRSizeCycles(t *testing.T, meter metric.Meter, prAuthor bool) {
	t.Helper()
	now := time.Now()
	in := now.Add(-time.Minute)
	created := now.Add(-48 * time.Hour)
	gql := collector.NewFixturePRClient([]collector.ExportedPRNode{
		{Number: 1, State: "MERGED", CreatedAt: created, MergedAt: &in, Additions: 120, Deletions: 30, Author: "alice"},
		{Number: 2, State: "MERGED", CreatedAt: created, MergedAt: &in, Additions: 4, Deletions: 4, Author: "renovate", AuthorIsBot: true},
		{Number: 3, State: "CLOSED", CreatedAt: created, ClosedAt: &in, Additions: 9000, Deletions: 12},
	})
	pr, err := collector.NewPRCollector(gql, config.PRFilters{},
		collector.PRCollectorConfig{LookbackDays: 30, PRAuthor: prAuthor}, meter, nil)
	if err != nil {
		t.Fatal(err)
	}
	repos := []discovery.Repo{{Owner: "acme", Name: "repo", FullName: "acme/repo"}}
	ctx := context.Background()
	if _, err := pr.Collect(ctx, "acme", repos, nil, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	last := map[string]time.Time{"acme/repo": now.Add(-time.Hour)}
	if _, err := pr.Collect(ctx, "acme", repos, last, now); err != nil {
		t.Fatal(err)
	}
}

// liveCheckAttributes returns the attribute keys live-check saw, from the
// statistics document.
func liveCheckAttributes(t *testing.T, report string) map[string]int {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(report))
	for {
		var doc struct {
			SeenRegistryMetrics    map[string]int `json:"seen_registry_metrics"`
			SeenRegistryAttributes map[string]int `json:"seen_registry_attributes"`
		}
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("live-check report: no statistics document: %v", err)
		}
		if doc.SeenRegistryMetrics != nil {
			return doc.SeenRegistryAttributes
		}
	}
}

// TestLiveCheck_PRAuthor pushes the PR size and close duration histograms
// over OTLP with collection.pr_author on and off: both conform, and only
// "on" carries github.pr.author.
func TestLiveCheck_PRAuthor(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(fmt.Sprintf("pr_author=%v", on), func(t *testing.T) {
			out, err := runLiveCheck(t, func(t *testing.T, p *exporter.Provider) {
				runPRSizeCycles(t, p.Meter(), on)
			})
			if err != nil {
				t.Fatalf("weaver registry live-check failed (schema violation): %v", err)
			}
			stats, err := liveCheckStatistics(out)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range prAuthorHistograms {
				if stats.SeenRegistryMetrics[name] == 0 {
					t.Errorf("live-check did not see %s", name)
				}
			}
			if got := liveCheckAttributes(t, out)[sc.AttrGitHubPrAuthor] > 0; got != on {
				t.Errorf("live-check saw %s = %v, want %v", sc.AttrGitHubPrAuthor, got, on)
			}
		})
	}
}

// scrapePRSize runs runPRSizeCycles on an exporter with the Prometheus
// bridge and returns the parsed /metrics scrape.
func scrapePRSize(t *testing.T, on bool) map[string]*dto.MetricFamily {
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
	runPRSizeCycles(t, p.Meter(), on)
	body := scrape(t, "http://"+addr+"/metrics", scrapeAccept["text"])
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse /metrics: %v\n%s", err, body)
	}
	return families
}

// TestPrometheusLiveCheck_PRAuthor checks /metrics with collection.pr_author
// on and off: live-check passes, the author values are the GraphQL logins,
// and "off" exposes no github.pr.author.
func TestPrometheusLiveCheck_PRAuthor(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(fmt.Sprintf("pr_author=%v", on), func(t *testing.T) {
			families := scrapePRSize(t, on)
			for _, name := range prAuthorHistograms {
				f := families[name]
				if f == nil {
					t.Fatalf("%s not exposed", name)
				}
				var authors []string
				for _, m := range f.GetMetric() {
					for _, l := range m.GetLabel() {
						if l.GetName() == sc.AttrGitHubPrAuthor {
							authors = append(authors, l.GetValue())
						}
					}
				}
				slices.Sort(authors)
				want := []string(nil)
				if on {
					want = []string{"alice", "renovate"}
				}
				if !slices.Equal(authors, want) {
					t.Errorf("%s: github.pr.author values = %v, want %v", name, authors, want)
				}
			}
			if _, err := runPrometheusLiveCheck(t, families); err != nil {
				t.Fatalf("weaver registry live-check failed (schema violation): %v", err)
			}
		})
	}
}
