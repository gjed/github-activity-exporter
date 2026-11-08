//go:build e2e

// Package e2e checks the generated Grafana dashboard against a real Prometheus
// 3.x fed by the exporter binary. It runs a mock GitHub API, pushes OTLP through
// a proxy that fails one push, and runs every panel and variable query.
package e2e

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// warmupWindow is the window in which every series must have two samples
// before the dashboard queries run.
const warmupWindow = "1m"

// pollDeadline bounds how long the exporter may take to fill Prometheus with
// enough samples for rate() and histogram_quantile().
const pollDeadline = 3 * time.Minute

func TestDashboardQueries(t *testing.T) {
	requireDocker(t)

	promURL := startPrometheus(t)
	mock := newMockGitHub(t)
	otlpURL := startOTLPProxy(t, promURL)
	exited := startExporter(t, mock.URL+"/api/v3", otlpURL, promURL+otlpMetricsPath)

	want := expectedSeriesNames(t)
	waitForSamples(t, promURL, want, exited)

	t.Run("registry metrics are in Prometheus", func(t *testing.T) {
		got := promLabelValues(t, promURL, "__name__")
		t.Logf("observed %d metric names in Prometheus: %s", len(got), strings.Join(got, ", "))
		var missing []string
		for _, name := range want {
			if !slices.Contains(got, name) {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			t.Errorf("registry metrics missing from Prometheus: %v", missing)
		}
	})

	t.Run("dashboard", func(t *testing.T) {
		runDashboardQueries(t, promURL)
	})

	for _, u := range mock.unhandledRequests() {
		t.Errorf("mock GitHub: unhandled request: %s", u)
	}
}

// registryMetric is the part of a registry metric or metric refinement the
// e2e reads.
type registryMetric struct {
	Name        string `yaml:"name"`
	Ref         string `yaml:"ref"`
	Annotations struct {
		Prometheus struct {
			Name string `yaml:"name"`
			Type string `yaml:"type"`
		} `yaml:"prometheus"`
	} `yaml:"annotations"`
}

// registryFile is the part of a registry/*.yaml definition file the e2e reads:
// own metrics, and refinements of metrics imported from the OTel semantic
// conventions (registry/telemetry.yaml), which carry their annotations.
type registryFile struct {
	Metrics           []registryMetric `yaml:"metrics"`
	MetricRefinements []registryMetric `yaml:"metric_refinements"`
}

// expectedSeriesNames returns the Prometheus series names of every registry
// metric. OTLP under NoTranslation keeps counter and gauge names bare, and a
// histogram is stored as classic <name>_bucket, _sum and _count series.
func expectedSeriesNames(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(), "registry", "*.yaml"))
	if err != nil {
		t.Fatalf("list registry: %v", err)
	}
	var metrics []registryMetric
	for _, f := range files {
		if filepath.Base(f) == "manifest.yaml" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read registry: %v", err)
		}
		var rf registryFile
		if err := yaml.Unmarshal(raw, &rf); err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		metrics = append(metrics, rf.Metrics...)
		metrics = append(metrics, rf.MetricRefinements...)
	}
	var names []string
	for _, m := range metrics {
		p := m.Annotations.Prometheus
		if p.Name == "" {
			t.Fatalf("registry metric %q has no annotations.prometheus.name", m.Name+m.Ref)
		}
		if p.Type == "histogram" {
			names = append(names, p.Name+"_bucket", p.Name+"_sum", p.Name+"_count")
		} else {
			names = append(names, p.Name)
		}
	}
	return names
}

// waitForSamples polls until every expected series has at least two samples
// in the window, so rate() and histogram_quantile() have data. One query per
// name: count_over_time drops __name__, so a combined query cannot say which
// name matched.
func waitForSamples(t *testing.T, promURL string, want []string, exited <-chan struct{}) {
	t.Helper()
	var missing []string
	deadline := time.Now().Add(pollDeadline)
	for {
		select {
		case <-exited:
			t.Fatal("exporter exited before Prometheus had enough samples")
		default:
		}
		missing = missing[:0]
		for _, name := range want {
			expr := fmt.Sprintf("count_over_time({__name__=%q}[%s]) >= 2", name, warmupWindow)
			body, err := promGet(promURL + "/api/v1/query?query=" + url.QueryEscape(expr))
			if n, cerr := checkResult(body); err != nil || cerr != nil || n == 0 {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %s, not every series has 2 samples in %s; missing: %v", pollDeadline, warmupWindow, missing)
		}
		time.Sleep(time.Second)
	}
}

// promLabelValues returns the values of a label across all series, from /api/v1/label/<name>/values.
func promLabelValues(t *testing.T, promURL, label string) []string {
	t.Helper()
	values, err := labelValues(promURL, label, "")
	if err != nil {
		t.Fatalf("label values for %s: %v", label, err)
	}
	return values
}
