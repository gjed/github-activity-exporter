package exporter_test

import (
	"context"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/github-activity-exporter/internal/exporter"
	"github.com/gjed/github-activity-exporter/internal/semconv"
)

// TestPrometheusBridge_NamesEqualRegistry records every registry metric,
// scrapes /metrics and asserts the exposed names follow the naming contract
// in registry/manifest.yaml: family name == registry name, and sample names
// add only _bucket/_sum/_count for histograms.
func TestPrometheusBridge_NamesEqualRegistry(t *testing.T) {
	const addr = "127.0.0.1:19092"
	p, err := exporter.New(context.Background(), exporter.Config{
		OTLPEndpoint:       "http://localhost:14318",
		CollectionInterval: 10 * time.Minute,
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

	recordAll(t, otel.GetMeterProvider().Meter("naming-test"))

	body := scrape(t, "http://"+addr+"/metrics")
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse /metrics: %v\n%s", err, body)
	}

	want := map[string]string{}
	for _, m := range semconv.Metrics {
		want[m.Name] = m.Instrument
	}

	var got []string
	for name := range families {
		// target_info and otel_scope_info describe the resource and scope,
		// not registry metrics.
		if name == "target_info" || name == "otel_scope_info" {
			continue
		}
		got = append(got, name)
	}
	sort.Strings(got)
	var wantNames []string
	for name := range want {
		wantNames = append(wantNames, name)
	}
	sort.Strings(wantNames)
	if strings.Join(got, "\n") != strings.Join(wantNames, "\n") {
		t.Fatalf("/metrics families != registry names\ngot:\n  %s\nwant:\n  %s\n/metrics:\n%s",
			strings.Join(got, "\n  "), strings.Join(wantNames, "\n  "), body)
	}

	// Every sample line must be the family name, or for histograms the family
	// name plus _bucket/_sum/_count.
	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sample := sampleName(line)
		if sample == "target_info" || sample == "otel_scope_info" {
			continue
		}
		if _, ok := want[sample]; ok {
			continue
		}
		ok := false
		for _, suffix := range []string{"_bucket", "_sum", "_count"} {
			if base, found := strings.CutSuffix(sample, suffix); found && want[base] == "histogram" {
				ok = true
			}
		}
		if !ok {
			t.Errorf("sample %q is neither a registry name nor a registry histogram + _bucket/_sum/_count", sample)
		}
	}
}

// recordAll creates one instrument per registry metric, with the instrument
// type and unit the registry declares, and records a single value.
func recordAll(t *testing.T, meter metric.Meter) {
	t.Helper()
	ctx := context.Background()
	for _, m := range semconv.Metrics {
		var err error
		switch m.Instrument {
		case "counter":
			var c metric.Int64Counter
			if c, err = meter.Int64Counter(m.Name, metric.WithUnit(m.Unit)); err == nil {
				c.Add(ctx, 1)
			}
		case "updowncounter":
			var c metric.Int64UpDownCounter
			if c, err = meter.Int64UpDownCounter(m.Name, metric.WithUnit(m.Unit)); err == nil {
				c.Add(ctx, 1)
			}
		case "gauge":
			var g metric.Float64Gauge
			if g, err = meter.Float64Gauge(m.Name, metric.WithUnit(m.Unit)); err == nil {
				g.Record(ctx, 1)
			}
		case "histogram":
			var h metric.Float64Histogram
			if h, err = meter.Float64Histogram(m.Name, metric.WithUnit(m.Unit)); err == nil {
				h.Record(ctx, 1)
			}
		default:
			t.Fatalf("metric %s: unknown instrument %q", m.Name, m.Instrument)
		}
		if err != nil {
			t.Fatalf("create %s: %v", m.Name, err)
		}
	}
}

// scrape GETs url asking for the classic text format with UTF-8 names.
func scrape(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/plain;version=1.0.0;escaping=allow-utf-8")
	// The bridge's HTTP server binds in a goroutine; retry until it listens.
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

// sampleName extracts the metric name from a text-format sample line: either
// a bare name (`name{...} v`, `name v`) or a quoted UTF-8 name
// (`{"name",...} v`).
func sampleName(line string) string {
	if strings.HasPrefix(line, `{"`) {
		rest := line[2:]
		return rest[:strings.Index(rest, `"`)]
	}
	return line[:strings.IndexAny(line, "{ ")]
}
