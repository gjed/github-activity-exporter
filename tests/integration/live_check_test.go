//go:build integration

// Package integration contains live-check tests that validate the exporter's
// emitted OTLP stream against the Weaver registry schema.
//
// Prerequisites:
//   - Docker (preferred) or `weaver` binary on PATH
//   - If using the binary: run scripts/install-weaver.sh
//
// Run:
//
//	go test -tags integration -timeout 120s ./tests/integration/...
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/gjed/github-activity-exporter/internal/exporter"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
)

const weaverVersion = "0.28.0"
const weaverImage = "otel/weaver:v" + weaverVersion

// TestLiveCheck_ConformsToSchema emits conforming metrics and asserts that
// `weaver registry live-check` reports no schema violations.
func TestLiveCheck_ConformsToSchema(t *testing.T) {
	if _, err := runLiveCheck(t, emitConformingMetrics); err != nil {
		t.Fatalf("weaver registry live-check failed (schema violation): %v", err)
	}
}

// TestLiveCheck_SchemaViolationDetected emits a metric with a wrong attribute
// type (integer instead of string for github.org) and asserts live-check
// returns non-zero.
func TestLiveCheck_SchemaViolationDetected(t *testing.T) {
	out, err := runLiveCheck(t, emitViolatingMetrics)
	requireViolation(t, out, err, "Attribute 'github.org' has type 'int'. Type should be 'string'.")
}

// TestLiveCheck_UndeclaredMetricDetected emits a metric the registry does not
// define and asserts live-check returns non-zero.
func TestLiveCheck_UndeclaredMetricDetected(t *testing.T) {
	out, err := runLiveCheck(t, emitUndeclaredMetric)
	requireViolation(t, out, err, "Metric does not exist in the registry.")
}

// TestLiveCheck_UndeclaredAttributeDetected emits github.pr.count with an
// attribute that the registry declares only on github.issue.count and asserts
// live-check returns non-zero.
func TestLiveCheck_UndeclaredAttributeDetected(t *testing.T) {
	out, err := runLiveCheck(t, emitUndeclaredAttribute)
	requireViolation(t, out, err, "Attribute 'github.issue.state' does not exist in the registry.")
}

// TestLiveCheck_NamesEqualRegistry emits every registry metric over OTLP and
// asserts that the metric names live-check saw are exactly the registry names
// (naming contract in registry/manifest.yaml). Metrics are emitted without
// attributes, so live-check reports missing required attributes and exits
// non-zero; attribute conformance is TestLiveCheck_ConformsToSchema's job.
func TestLiveCheck_NamesEqualRegistry(t *testing.T) {
	out, _ := runLiveCheck(t, emitAllRegistryMetrics)
	stats, err := liveCheckStatistics(out)
	if err != nil {
		t.Fatalf("live-check report: %v", err)
	}

	var unseen []string
	for _, m := range sc.Metrics {
		if stats.SeenRegistryMetrics[m.Name] == 0 {
			unseen = append(unseen, m.Name)
		}
	}
	var extra []string
	for name := range stats.SeenNonRegistryMetrics {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	if len(unseen) > 0 || len(extra) > 0 {
		t.Fatalf("OTLP metric names != registry names\nregistry metrics not seen: %v\nnon-registry metrics seen: %v", unseen, extra)
	}
}

// liveCheckStats is the subset of the live-check JSON statistics object
// this suite reads.
type liveCheckStats struct {
	SeenRegistryMetrics    map[string]int `json:"seen_registry_metrics"`
	SeenNonRegistryMetrics map[string]int `json:"seen_non_registry_metrics"`
}

// liveCheckStatistics decodes the stream of JSON documents live-check writes
// with --format json and returns the final statistics document.
func liveCheckStatistics(report string) (liveCheckStats, error) {
	dec := json.NewDecoder(strings.NewReader(report))
	for {
		var stats liveCheckStats
		if err := dec.Decode(&stats); err != nil {
			if errors.Is(err, io.EOF) {
				return liveCheckStats{}, errors.New("no statistics document (seen_registry_metrics) in the report")
			}
			return liveCheckStats{}, fmt.Errorf("decode: %w", err)
		}
		// Sample documents (resource, scope, metric) lack this key.
		if stats.SeenRegistryMetrics != nil {
			return stats, nil
		}
	}
}

// requireViolation asserts that live-check exited non-zero and that its report
// contains the expected violation message, so an unrelated weaver failure
// cannot make a negative test pass.
func requireViolation(t *testing.T, out string, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected weaver live-check to return non-zero, but it exited 0")
	}
	if !strings.Contains(out, msg) {
		t.Fatalf("weaver live-check exited with %v but its report lacks %q", err, msg)
	}
}

// runLiveCheck starts `weaver registry live-check` as an OTLP/gRPC receiver,
// waits for it to listen, pushes the metrics recorded by emit to it, and
// returns the live-check report (stdout) and exit error once it stops.
func runLiveCheck(t *testing.T, emit func(*testing.T, *exporter.Provider)) (string, error) {
	t.Helper()
	runner := requireWeaverRunner(t)

	grpcPort := freePort(t)
	adminPort := freePort(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := runner.liveCheckCmd(ctx, grpcPort, adminPort)
	var report bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &report)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start weaver live-check: %v", err)
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()

	// The receiver must be listening before metrics are pushed, otherwise the
	// export is lost and live-check sees no telemetry.
	addr := fmt.Sprintf("127.0.0.1:%d", grpcPort)
	if err := waitForListener(addr, waitErr, 60*time.Second); err != nil {
		t.Fatalf("weaver live-check receiver on %s: %v", addr, err)
	}

	cfg := exporter.Config{
		OTLPEndpoint:       addr,
		Protocol:           exporter.ProtocolGRPC,
		TLSInsecure:        true,      // plaintext gRPC to the local receiver
		CollectionInterval: time.Hour, // export only via ForceFlush
	}
	p, err := exporter.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("exporter.New: %v", err)
	}

	emit(t, p)

	// ForceFlush pushes pending metric data immediately rather than waiting
	// for the next PeriodicReader cycle.
	flushCtx, flushCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer flushCancel()
	if err := p.ForceFlush(flushCtx); err != nil {
		t.Fatalf("ForceFlush to weaver live-check: %v", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = p.Shutdown(shutdownCtx)

	// Ask live-check to stop and render its report now that all telemetry
	// has been delivered.
	stopURL := fmt.Sprintf("http://127.0.0.1:%d/stop", adminPort)
	resp, err := http.Post(stopURL, "text/plain", nil)
	if err != nil {
		t.Fatalf("POST %s: %v", stopURL, err)
	}
	_ = resp.Body.Close()

	select {
	case err := <-waitErr:
		return report.String(), err
	case <-ctx.Done():
		t.Fatalf("weaver live-check did not exit: %v", ctx.Err())
		return "", nil
	}
}

// waitForListener polls addr until it accepts TCP connections, the process
// exits, or the timeout elapses.
func waitForListener(addr string, exited <-chan error, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			return fmt.Errorf("weaver exited before listening: %v", err)
		default:
		}
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("not listening after %s", timeout)
}

// ── weaver runner ─────────────────────────────────────────────────────────────
// weaverRunner abstracts running weaver via Docker or a local binary.
type weaverRunner struct {
	useDocker   bool
	registryDir string // absolute path to registry/ on the host
}

// requireWeaverRunner returns a weaverRunner, preferring Docker when available.
// Skips the test if neither Docker nor the weaver binary is found.
func requireWeaverRunner(t *testing.T) weaverRunner {
	t.Helper()

	// Locate the registry/ directory relative to this test file.
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	registryDir, err := filepath.Abs(filepath.Join(repoRoot, "registry"))
	if err != nil {
		t.Fatalf("resolve registry path: %v", err)
	}

	// Prefer Docker.
	if dockerBin, err := exec.LookPath("docker"); err == nil {
		if out, err := exec.Command(dockerBin, "info").CombinedOutput(); err == nil && !strings.Contains(string(out), "ERROR") {
			t.Logf("using Docker to run weaver (%s)", weaverImage)
			return weaverRunner{useDocker: true, registryDir: registryDir}
		}
	}

	// Fall back to local binary.
	if _, err := exec.LookPath("weaver"); err == nil {
		t.Logf("using local weaver binary")
		return weaverRunner{useDocker: false, registryDir: registryDir}
	}

	t.Skip("neither Docker nor weaver binary found — run scripts/install-weaver.sh or ensure Docker is running")
	return weaverRunner{}
}

// liveCheckCmd builds the exec.Cmd for `weaver registry live-check`
// listening for OTLP/gRPC on grpcPort. When running via Docker, --network host
// is used so the test process can reach the receiver on localhost.
func (r weaverRunner) liveCheckCmd(ctx context.Context, grpcPort, adminPort int) *exec.Cmd {
	args := []string{
		"registry", "live-check",
		"--otlp-grpc-port", fmt.Sprint(grpcPort),
		"--admin-port", fmt.Sprint(adminPort),
		"--inactivity-timeout", "60", // stopped via the admin /stop endpoint
		// Match samples against the v2 registry: an attribute not declared on
		// the matched metric is reported as a violation.
		"--v2",
		// JSON report: the name check reads its statistics document.
		"--format", "json",
	}
	configFile := filepath.Join(filepath.Dir(r.registryDir), ".weaver.toml")
	if r.useDocker {
		docker := []string{
			"run", "--rm",
			"--network", "host",
			"--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/registry,readonly", r.registryDir),
			"--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/.weaver.toml,readonly", configFile),
			"--env", "HOME=/tmp/weaver",
			weaverImage,
		}
		args = append(append(docker, args...), "-r", "/workspace/registry/", "--config", "/workspace/.weaver.toml")
		return exec.CommandContext(ctx, "docker", args...)
	}
	return exec.CommandContext(ctx, "weaver", append(args, "-r", r.registryDir+"/", "--config", configFile)...)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// emitConformingMetrics records metrics using the correct attribute types
// as defined in the Weaver registry.
func emitConformingMetrics(t *testing.T, p *exporter.Provider) {
	t.Helper()
	meter := p.MeterProvider().Meter("integration-test")

	prCount, err := meter.Int64UpDownCounter(sc.MetricGitHubPrCount, metric.WithUnit("{pr}"))
	if err != nil {
		t.Fatalf("create pr.count: %v", err)
	}
	prCount.Add(context.Background(), 5,
		metric.WithAttributes(
			attribute.String(sc.AttrExporterTarget, "test-org/test-repo"),
			attribute.String(sc.AttrGitHubOrg, "test-org"),
			attribute.String(sc.AttrGitHubRepo, "test-repo"),
			attribute.String(sc.AttrGitHubPrState, sc.AttrGitHubPrStateOpen),
		),
	)

	scrapeDur, err := meter.Float64Histogram(sc.MetricGitHubExporterScrapeDuration, metric.WithUnit("s"))
	if err != nil {
		t.Fatalf("create scrape.duration: %v", err)
	}
	scrapeDur.Record(context.Background(), 1.23,
		metric.WithAttributes(
			attribute.String(sc.AttrExporterTarget, "test-org/test-repo"),
		),
	)

	// Self-metrics use the global MeterProvider set by exporter.New.
	self, err := exporter.NewSelfMetrics()
	if err != nil {
		t.Fatalf("NewSelfMetrics: %v", err)
	}
	self.RecordAPIError(context.Background(), "test-org/test-repo", sc.AttrGitHubApiEndpointGraphQL)
	self.RecordRateLimit(context.Background(), "test-org/test-repo", 4999, time.Now().Add(time.Hour))
}

// emitViolatingMetrics records github.pr.count with github.org set to an
// integer, which violates the registry schema (type: string).
func emitViolatingMetrics(t *testing.T, p *exporter.Provider) {
	t.Helper()
	meter := p.MeterProvider().Meter("integration-test-bad")

	prCount, err := meter.Int64UpDownCounter(sc.MetricGitHubPrCount, metric.WithUnit("{pr}"))
	if err != nil {
		t.Fatalf("create pr.count: %v", err)
	}
	// VIOLATION: github.org must be string, not int.
	prCount.Add(context.Background(), 3,
		metric.WithAttributes(
			attribute.String(sc.AttrExporterTarget, "test-org/test-repo"),
			attribute.Int(sc.AttrGitHubOrg, 42), // wrong type: int instead of string
			attribute.String(sc.AttrGitHubRepo, "test-repo"),
			attribute.String(sc.AttrGitHubPrState, sc.AttrGitHubPrStateOpen),
		),
	)
}

// conformingPRCountAttrs are the attributes github.pr.count requires.
func conformingPRCountAttrs() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(sc.AttrExporterTarget, "test-org/test-repo"),
		attribute.String(sc.AttrGitHubOrg, "test-org"),
		attribute.String(sc.AttrGitHubRepo, "test-repo"),
		attribute.String(sc.AttrGitHubPrState, sc.AttrGitHubPrStateOpen),
	}
}

// emitUndeclaredMetric records a metric that is not in the registry.
func emitUndeclaredMetric(t *testing.T, p *exporter.Provider) {
	t.Helper()
	meter := p.MeterProvider().Meter("integration-test-undeclared-metric")

	// VIOLATION: github.pr.merged is not defined in registry/signals.yaml.
	c, err := meter.Int64Counter("github.pr.merged", metric.WithUnit("{pr}"))
	if err != nil {
		t.Fatalf("create github.pr.merged: %v", err)
	}
	c.Add(context.Background(), 1, metric.WithAttributes(conformingPRCountAttrs()...))
}

// emitUndeclaredAttribute records github.pr.count with every required
// attribute plus github.issue.state, which the registry declares only on
// github.issue.count.
func emitUndeclaredAttribute(t *testing.T, p *exporter.Provider) {
	t.Helper()
	meter := p.MeterProvider().Meter("integration-test-undeclared-attribute")

	prCount, err := meter.Int64UpDownCounter(sc.MetricGitHubPrCount, metric.WithUnit("{pr}"))
	if err != nil {
		t.Fatalf("create pr.count: %v", err)
	}
	// VIOLATION: github.issue.state is not in github.pr.count's attributes.
	attrs := append(conformingPRCountAttrs(),
		attribute.String(sc.AttrGitHubIssueState, sc.AttrGitHubIssueStateOpen))
	prCount.Add(context.Background(), 3, metric.WithAttributes(attrs...))
}

// emitAllRegistryMetrics records one value on every registry metric, using
// the instrument type and unit the registry declares.
func emitAllRegistryMetrics(t *testing.T, p *exporter.Provider) {
	t.Helper()
	ctx := context.Background()
	meter := p.MeterProvider().Meter("integration-test-all-metrics")
	for _, m := range sc.Metrics {
		var err error
		unit := metric.WithUnit(m.Unit)
		switch m.Instrument {
		case "counter":
			var c metric.Int64Counter
			if c, err = meter.Int64Counter(m.Name, unit); err == nil {
				c.Add(ctx, 1)
			}
		case "updowncounter":
			var c metric.Int64UpDownCounter
			if c, err = meter.Int64UpDownCounter(m.Name, unit); err == nil {
				c.Add(ctx, 1)
			}
		case "gauge":
			var g metric.Float64Gauge
			if g, err = meter.Float64Gauge(m.Name, unit); err == nil {
				g.Record(ctx, 1)
			}
		case "histogram":
			var h metric.Float64Histogram
			if h, err = meter.Float64Histogram(m.Name, unit); err == nil {
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
