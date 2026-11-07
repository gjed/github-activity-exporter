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

// pollDeadline bounds how long the exporter may take to fill Prometheus with
// enough samples for rate() and histogram_quantile().
const pollDeadline = 3 * time.Minute

func TestDashboardQueries(t *testing.T) {
	requireDocker(t)

	promURL := startPrometheus(t)
	mock := newMockGitHub(t)
	otlpURL := startOTLPProxy(t, promURL)
	exited := startExporter(t, mock.URL+"/api/v3", otlpURL)

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

// signalsFile is the part of registry/signals.yaml the e2e reads.
type signalsFile struct {
	Metrics []struct {
		Name        string `yaml:"name"`
		Annotations struct {
			Prometheus struct {
				Name string `yaml:"name"`
				Type string `yaml:"type"`
			} `yaml:"prometheus"`
		} `yaml:"annotations"`
	} `yaml:"metrics"`
}

// expectedSeriesNames returns the Prometheus series names of every registry
// metric. OTLP under NoTranslation keeps counter and gauge names bare, and a
// histogram is stored as classic <name>_bucket, _sum and _count series.
func expectedSeriesNames(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(), "registry", "signals.yaml"))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var sig signalsFile
	if err := yaml.Unmarshal(raw, &sig); err != nil {
		t.Fatalf("parse registry: %v", err)
	}
	var names []string
	for _, m := range sig.Metrics {
		p := m.Annotations.Prometheus
		if p.Name == "" {
			t.Fatalf("registry metric %q has no annotations.prometheus.name", m.Name)
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
			expr := fmt.Sprintf("count_over_time({__name__=%q}[%s]) >= 2", name, rateWindow)
			body, err := promGet(promURL + "/api/v1/query?query=" + url.QueryEscape(expr))
			if n, cerr := checkResult(body); err != nil || cerr != nil || n == 0 {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %s, not every series has 2 samples in %s; missing: %v", pollDeadline, rateWindow, missing)
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
