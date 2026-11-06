//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/gjed/github-activity-exporter/internal/exporter"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
)

// scrapeAccept are the Accept headers the /metrics tests scrape with.
// /metrics does not serve OpenMetrics, so the openmetrics request falls back
// to the text format: it covers that fallback, not the OpenMetrics encoding.
var scrapeAccept = map[string]string{
	// Prometheus 3.x and Alloy default scrape: OpenMetrics first.
	"openmetrics": "application/openmetrics-text;version=1.0.0;escaping=allow-utf-8",
	"text":        "text/plain;version=1.0.0;escaping=allow-utf-8",
}

// TestPrometheusLiveCheck_NamesAndTypes records every registry metric,
// scrapes /metrics and asserts that live-check sees exactly the registry
// metric names, each exposed with its annotations.prometheus.type. Metrics are
// recorded without attributes, so missing required attributes are expected.
func TestPrometheusLiveCheck_NamesAndTypes(t *testing.T) {
	for name, accept := range scrapeAccept {
		t.Run(name, func(t *testing.T) {
			out, _ := runPrometheusLiveCheck(t, scrapeAllRegistryMetrics(t, accept))
			requireRegistryNames(t, out)
			requireOnlyMissingRequiredAttributes(t, out)
		})
	}
}

// TestPrometheusLiveCheck_SuffixedNameDetected exposes github.pr.automerged
// as github.pr.automerged_total and asserts live-check rejects it.
func TestPrometheusLiveCheck_SuffixedNameDetected(t *testing.T) {
	families := scrapeAllRegistryMetrics(t, scrapeAccept["text"])
	mf := families[sc.MetricGitHubPrAutomerged]
	mf.Name = new(sc.MetricGitHubPrAutomerged + "_total")
	delete(families, sc.MetricGitHubPrAutomerged)
	families[mf.GetName()] = mf

	out, err := runPrometheusLiveCheck(t, families)
	requireViolation(t, out, err, "Metric does not exist in the registry.")
	stats, err := liveCheckStatistics(out)
	if err != nil {
		t.Fatalf("live-check report: %v", err)
	}
	if stats.SeenNonRegistryMetrics[mf.GetName()] == 0 {
		t.Fatalf("live-check did not report %s as a non-registry metric: %v", mf.GetName(), stats.SeenNonRegistryMetrics)
	}
}

// TestPrometheusLiveCheck_WrongTypeDetected exposes the counter
// github.pr.automerged as a gauge and asserts live-check rejects it.
func TestPrometheusLiveCheck_WrongTypeDetected(t *testing.T) {
	families := scrapeAllRegistryMetrics(t, scrapeAccept["text"])
	families[sc.MetricGitHubPrAutomerged].Type = dto.MetricType_GAUGE.Enum()

	out, err := runPrometheusLiveCheck(t, families)
	requireViolation(t, out, err, "Metric 'github.pr.automerged' is exposed as Prometheus type 'gauge'; annotations.prometheus.type is 'counter'.")
}

// scrapeAllRegistryMetrics records every registry metric on an exporter with
// the Prometheus bridge enabled and returns the parsed /metrics scrape.
func scrapeAllRegistryMetrics(t *testing.T, accept string) map[string]*dto.MetricFamily {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	p, err := exporter.New(context.Background(), exporter.Config{
		OTLPEndpoint:       "http://127.0.0.1:1", // unreachable: the 1h interval never fires; Shutdown's final push fails and is ignored
		CollectionInterval: time.Hour,
		PrometheusEnabled:  true,
		PrometheusAddr:     addr,
	}, nil)
	if err != nil {
		t.Fatalf("exporter.New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	})

	emitAllRegistryMetrics(t, p)

	body := scrape(t, "http://"+addr+"/metrics", accept)
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse /metrics: %v\n%s", err, body)
	}
	return families
}

// scrape GETs url with the given Accept header, retrying until the bridge's
// HTTP server listens.
func scrape(t *testing.T, url, accept string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", accept)
	var resp *http.Response
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, err = http.DefaultClient.Do(req)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	return string(b)
}

// runPrometheusLiveCheck converts families to live-check samples, runs
// `weaver registry live-check` on them with .weaver-prometheus.toml and
// policies/live_check/, and returns the report and exit error.
func runPrometheusLiveCheck(t *testing.T, families map[string]*dto.MetricFamily) (string, error) {
	t.Helper()
	runner := requireWeaverRunner(t)

	samples, err := json.MarshalIndent(expositionSamples(t, families), "", "  ")
	if err != nil {
		t.Fatalf("marshal samples: %v", err)
	}
	samplesFile := filepath.Join(t.TempDir(), "samples.json")
	if err := os.WriteFile(samplesFile, samples, 0o644); err != nil {
		t.Fatalf("write samples: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := runner.liveCheckFileCmd(ctx, samplesFile)
	var report bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &report)
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	return report.String(), err
}

// liveCheckFileCmd builds the exec.Cmd for `weaver registry live-check` on a
// JSON samples file, with the /metrics config and advice policies.
func (r weaverRunner) liveCheckFileCmd(ctx context.Context, samplesFile string) *exec.Cmd {
	root := filepath.Dir(r.registryDir)
	args := []string{
		"registry", "live-check",
		"--v2",
		"--input-format", "json",
		"--format", "json",
	}
	if r.useDocker {
		docker := []string{
			"run", "--rm",
			"--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/registry,readonly", r.registryDir),
			"--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/policies,readonly", filepath.Join(root, "policies")),
			"--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/.weaver-prometheus.toml,readonly", filepath.Join(root, ".weaver-prometheus.toml")),
			"--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/samples.json,readonly", samplesFile),
			"--env", "HOME=/tmp/weaver",
			weaverImage,
		}
		args = append(append(docker, args...),
			"-r", "/workspace/registry/",
			"--config", "/workspace/.weaver-prometheus.toml",
			"--advice-policies", "/workspace/policies/live_check/",
			"--input-source", "/workspace/samples.json")
		return exec.CommandContext(ctx, "docker", args...)
	}
	return exec.CommandContext(ctx, "weaver", append(args,
		"-r", r.registryDir+"/",
		"--config", filepath.Join(root, ".weaver-prometheus.toml"),
		"--advice-policies", filepath.Join(root, "policies", "live_check")+"/",
		"--input-source", samplesFile)...)
}

// ── exposition → live-check samples ──────────────────────────────────────────

const scopeLabelPrefix = "otel_scope_"

// expositionSamples converts a parsed /metrics scrape into live-check JSON
// samples: target_info becomes the resource, the otel_scope_* labels become an
// instrumentation scope sample followed by that scope's metrics, and each
// family becomes a metric whose instrument is the exposed Prometheus type
// (counter, gauge, histogram). The exposition has no unit, so unit is empty.
// Live-check pairs a metric with the resource and scope samples before it.
func expositionSamples(t *testing.T, families map[string]*dto.MetricFamily) []any {
	t.Helper()
	var samples []any

	if ti, ok := families["target_info"]; ok {
		for _, m := range ti.GetMetric() {
			samples = append(samples, map[string]any{
				"resource": map[string]any{"attributes": resourceAttributes(m.GetLabel())},
			})
		}
	}

	type scopeMetrics struct {
		sample  map[string]any
		metrics map[string]map[string]any // family name → metric sample
	}
	scopes := map[string]*scopeMetrics{}

	for name, mf := range families {
		if name == "target_info" || name == "otel_scope_info" {
			continue
		}
		for _, m := range mf.GetMetric() {
			scope, attrs := splitScopeLabels(m.GetLabel())
			key, _ := json.Marshal(scope)
			s, ok := scopes[string(key)]
			if !ok {
				s = &scopeMetrics{sample: scope, metrics: map[string]map[string]any{}}
				scopes[string(key)] = s
			}
			metric, ok := s.metrics[name]
			if !ok {
				metric = map[string]any{
					"name":        name,
					"instrument":  strings.ToLower(mf.GetType().String()),
					"unit":        "",
					"data_points": []any{},
				}
				s.metrics[name] = metric
			}
			metric["data_points"] = append(metric["data_points"].([]any), dataPoint(t, mf.GetType(), m, attrs))
		}
	}

	keys := make([]string, 0, len(scopes))
	for k := range scopes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s := scopes[k]
		samples = append(samples, map[string]any{"instrumentation_scope": s.sample})
		names := make([]string, 0, len(s.metrics))
		for name := range s.metrics {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			samples = append(samples, map[string]any{"metric": s.metrics[name]})
		}
	}
	return samples
}

// splitScopeLabels returns the instrumentation scope sample described by the
// otel_scope_* labels, and the remaining labels as data point attributes.
func splitScopeLabels(labels []*dto.LabelPair) (map[string]any, []any) {
	scope := map[string]any{"name": "", "attributes": []any{}}
	attrs := []any{}
	for _, l := range labels {
		key, isScope := strings.CutPrefix(l.GetName(), scopeLabelPrefix)
		switch {
		case !isScope:
			attrs = append(attrs, stringAttribute(l.GetName(), l.GetValue()))
		case key == "name":
			scope["name"] = l.GetValue()
		case key == "version" || key == "schema_url":
			if l.GetValue() != "" {
				scope[key] = l.GetValue()
			}
		default:
			scope["attributes"] = append(scope["attributes"].([]any), stringAttribute(key, l.GetValue()))
		}
	}
	return scope, attrs
}

// resourceAttributes converts target_info labels to resource attributes.
// The bridge writes every attribute value as a label string: an int as its
// decimal form, a string slice as a JSON array. Live-check checks types, so
// those two encodings are decoded back (process.pid is int and
// process.command_args is string[] in the registry).
// SIMPLIFIED: a string attribute that holds only digits or a JSON string
// array would be decoded too; read the types from the resolved registry if a
// resource attribute ever does.
func resourceAttributes(labels []*dto.LabelPair) []any {
	attrs := []any{}
	for _, l := range labels {
		name, value := l.GetName(), l.GetValue()
		var list []string
		switch n, err := strconv.ParseInt(value, 10, 64); {
		case err == nil:
			attrs = append(attrs, map[string]any{"name": name, "value": n, "type": "int"})
		case strings.HasPrefix(value, "[") && json.Unmarshal([]byte(value), &list) == nil:
			attrs = append(attrs, map[string]any{"name": name, "value": list, "type": "string[]"})
		default:
			attrs = append(attrs, stringAttribute(name, value))
		}
	}
	return attrs
}

func stringAttribute(name, value string) map[string]any {
	return map[string]any{"name": name, "value": value, "type": "string"}
}

// dataPoint converts one exposed series to a live-check data point. A classic
// histogram's _bucket/_sum/_count series become one histogram data point with
// per-bucket (not cumulative) counts.
func dataPoint(t *testing.T, typ dto.MetricType, m *dto.Metric, attrs []any) map[string]any {
	t.Helper()
	switch typ {
	case dto.MetricType_COUNTER:
		return map[string]any{"attributes": attrs, "value": m.GetCounter().GetValue()}
	case dto.MetricType_GAUGE:
		return map[string]any{"attributes": attrs, "value": m.GetGauge().GetValue()}
	case dto.MetricType_UNTYPED:
		return map[string]any{"attributes": attrs, "value": m.GetUntyped().GetValue()}
	case dto.MetricType_HISTOGRAM:
		h := m.GetHistogram()
		bounds := []float64{}
		counts := []uint64{}
		var prev uint64
		for _, b := range h.GetBucket() {
			if math.IsInf(b.GetUpperBound(), +1) {
				continue
			}
			bounds = append(bounds, b.GetUpperBound())
			counts = append(counts, b.GetCumulativeCount()-prev)
			prev = b.GetCumulativeCount()
		}
		counts = append(counts, h.GetSampleCount()-prev) // (last bound, +Inf]
		return map[string]any{
			"attributes":      attrs,
			"count":           h.GetSampleCount(),
			"sum":             h.GetSampleSum(),
			"explicit_bounds": bounds,
			"bucket_counts":   counts,
		}
	default:
		t.Fatalf("unsupported Prometheus type %s", typ)
		return nil
	}
}
