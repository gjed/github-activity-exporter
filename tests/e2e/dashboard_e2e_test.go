//go:build e2e

// Package e2e checks the generated Grafana dashboard against a real Prometheus
// 3.x fed by the exporter binary. It runs a mock GitHub API, pushes OTLP through
// a proxy that fails one push, and runs every panel and variable query.
package e2e

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	exited := startExporter(t, mock.URL+"/api/v3", otlpURL, promURL+otlpMetricsPath, testCollectionInterval)

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

// continuitySteps is how many dashboard steps TestDashboardContinuity samples.
const continuitySteps = 12

// longCollectionInterval is collection_interval 15m, scaled: longer than the
// dashboard's default Min interval, so before export.interval most windows
// held no push.
const longCollectionInterval = 15 * time.Minute / timeScale

// TestDashboardContinuity runs the exporter with collection_interval 15m and
// the default export.interval, both scaled. Every rate() panel must have a
// value at every step of the dashboard's default Min interval, and the cycle
// counter must change once per cycle, not once per push.
func TestDashboardContinuity(t *testing.T) {
	requireDocker(t)

	promURL := startPrometheus(t)
	mock := newMockGitHub(t)
	otlpURL := startOTLPProxy(t, promURL)
	exited := startExporter(t, mock.URL+"/api/v3", otlpURL, promURL+otlpMetricsPath, longCollectionInterval)
	waitForSamples(t, promURL, expectedSeriesNames(t), exited)

	d, vars := loadDashboard(t)
	for _, v := range d.Templating.List {
		if v.Type == "query" {
			if err := resolveVariable(promURL, v, vars); err != nil {
				t.Fatalf("variable %q: %v", v.Name, err)
			}
		}
	}
	minStep, err := time.ParseDuration(vars["min_step"])
	if err != nil {
		t.Fatalf("min_step: %v", err)
	}
	window, err := rateInterval(vars["min_step"])
	if err != nil {
		t.Fatal(err)
	}
	step := minStep / timeScale
	start := time.Now()
	time.Sleep(continuitySteps * step)
	end := time.Now()
	t.Logf("sampling %s..%s, step %s (min_step %s scaled), window %s", start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), step, minStep, window)

	withTime := map[string]string{"__rate_interval": promDuration(window), "__interval": promDuration(window)}
	for k, v := range vars {
		withTime[k] = v
	}
	var checked int
	for _, pt := range collectTargets(d.Panels) {
		if !strings.Contains(pt.expr, "rate(") || strings.HasPrefix(pt.expr, "histogram_quantile(") {
			continue
		}
		checked++
		t.Run(fmt.Sprintf("%s/%s", pt.panel, pt.refID), func(t *testing.T) {
			expr, err := expandVars(pt.expr, withTime)
			if err != nil {
				t.Fatal(err)
			}
			got, want, err := stepsWithValue(promURL, expr, start, end, step)
			if err != nil {
				t.Fatalf("%v\nexpr: %s", err, expr)
			}
			if got != want {
				t.Fatalf("value at %d of %d steps\nexpr: %s", got, want, expr)
			}
			t.Logf("OK panel %q refId %s: value at %d/%d steps", pt.panel, pt.refID, got, want)
		})
	}
	if checked == 0 {
		t.Fatal("dashboard has no rate() panel")
	}

	t.Run("pushes repeat the cycle count", func(t *testing.T) {
		run := promDuration(end.Sub(start))
		samples := instantValue(t, promURL, fmt.Sprintf(`count_over_time({"github_exporter.scrape.duration_count"}[%s])`, run))
		changes := instantValue(t, promURL, fmt.Sprintf(`changes({"github_exporter.scrape.duration_count"}[%s])`, run))
		cycles := float64(end.Sub(start)/longCollectionInterval) + 1
		t.Logf("over %s: %v samples of github_exporter.scrape.duration_count, %v changes, at most %v cycles", run, samples, changes, cycles)
		if changes > cycles {
			t.Errorf("count changed %v times in %v cycles: repeated pushes add up", changes, cycles)
		}
		if samples < 4*cycles {
			t.Errorf("%v samples for %v cycles: not pushed every export.interval", samples, cycles)
		}
	})

	for _, u := range mock.unhandledRequests() {
		t.Errorf("mock GitHub: unhandled request: %s", u)
	}
}

// instantValue returns the value of a single-series instant query.
func instantValue(t *testing.T, promURL, expr string) float64 {
	t.Helper()
	body, err := promGet(promURL + "/api/v1/query?" + url.Values{"query": {expr}}.Encode())
	if err != nil {
		t.Fatalf("%s: %v: %s", expr, err, truncate(body, 800))
	}
	var resp struct {
		Data struct {
			Result []struct {
				Value [2]any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || len(resp.Data.Result) != 1 {
		t.Fatalf("%s: want one series: %v: %s", expr, err, truncate(body, 800))
	}
	f, err := strconv.ParseFloat(fmt.Sprint(resp.Data.Result[0].Value[1]), 64)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return f
}

// stepsWithValue runs expr as a range query and returns how many steps have a
// non-NaN value in at least one series, out of the steps in [start, end].
func stepsWithValue(promURL, expr string, start, end time.Time, step time.Duration) (int, int, error) {
	q := url.Values{
		"query": {expr},
		"start": {fmt.Sprintf("%.3f", float64(start.UnixMilli())/1000)},
		"end":   {fmt.Sprintf("%.3f", float64(end.UnixMilli())/1000)},
		"step":  {fmt.Sprintf("%.3f", step.Seconds())},
	}
	body, err := promGet(promURL + "/api/v1/query_range?" + q.Encode())
	if err != nil {
		return 0, 0, fmt.Errorf("%v: %s", err, truncate(body, 800))
	}
	var resp struct {
		Status string `json:"status"`
		Data   struct {
			Result []struct {
				Values [][2]any `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || resp.Status != "success" {
		return 0, 0, fmt.Errorf("decode: %v: %s", err, truncate(body, 800))
	}
	steps := make(map[string]bool)
	for _, s := range resp.Data.Result {
		for _, v := range s.Values {
			if f, err := strconv.ParseFloat(fmt.Sprint(v[1]), 64); err == nil && !math.IsNaN(f) {
				steps[fmt.Sprint(v[0])] = true
			}
		}
	}
	return len(steps), int((end.UnixMilli()-start.UnixMilli())/step.Milliseconds()) + 1, nil
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
