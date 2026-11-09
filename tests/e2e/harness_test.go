//go:build e2e

package e2e

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// promImage is the Prometheus 3.x image the e2e runs against, with UTF-8 names
// (the 3.x default) and the OTLP NoTranslation strategy set in its config.
// renovate: datasource=docker depName=prom/prometheus
const promImage = "prom/prometheus:v3.16.0"

// otlpMetricsPath is the Prometheus OTLP receiver path.
const otlpMetricsPath = "/api/v1/otlp/v1/metrics"

// requireDocker skips the test when Docker is unavailable, and fails in CI
// where Docker must be there.
func requireDocker(t *testing.T) {
	t.Helper()
	if err := exec.Command("docker", "info").Run(); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("docker is required in CI: %v", err)
		}
		t.Skipf("docker unavailable: %v", err)
	}
}

// startPrometheus runs the pinned Prometheus with the OTLP receiver and
// NoTranslation, otherwise stock (no out-of-order window), and returns its
// base URL once /-/ready answers.
func startPrometheus(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// The image runs as nobody and t.TempDir is 0700, so open the config dir for the container.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "otlp:\n  translation_strategy: NoTranslation\n"
	if err := os.WriteFile(filepath.Join(dir, "prometheus.yml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	name := fmt.Sprintf("github-activity-exporter-e2e-%d", port)
	out, err := exec.Command("docker", "run", "-d", "--name", name,
		"-p", fmt.Sprintf("127.0.0.1:%d:9090", port),
		"-v", dir+":/etc/prometheus:ro",
		promImage,
		"--config.file=/etc/prometheus/prometheus.yml",
		"--web.enable-otlp-receiver",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run %s: %v\n%s", promImage, err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	if !waitFor(60*time.Second, func() bool {
		resp, err := http.Get(base + "/-/ready")
		if err != nil {
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == http.StatusOK
	}) {
		t.Fatalf("prometheus %s not ready after 60s", base)
	}
	return base
}

// startOTLPProxy fronts Prometheus' OTLP endpoint. It rejects the first push
// with HTTP 400, which the OTLP HTTP exporter does not retry, so the exporter
// returns an error and errorCountingExporter.Export (internal/exporter/error_counting.go)
// increments github_exporter.otlp.errors. The later pushes are forwarded.
func startOTLPProxy(t *testing.T, promURL string) string {
	t.Helper()
	target, err := url.Parse(promURL)
	if err != nil {
		t.Fatal(err)
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	var pushes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if pushes.Add(1) == 1 {
			http.Error(w, "e2e: first OTLP push rejected", http.StatusBadRequest)
			return
		}
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + otlpMetricsPath
}

// startExporter builds the exporter binary and runs it with a temp config that
// uses apiURL (the mock REST root, .../api/v3), collects every
// collectionInterval and pushes OTLP to otlpURL every testExportInterval. Its
// output goes to the test log. Self-telemetry metrics are on and inherit
// export.otlp, so they travel in the same pushes. The returned channel closes
// once the process has exited.
func startExporter(t *testing.T, apiURL, otlpURL string, collectionInterval time.Duration) <-chan struct{} {
	t.Helper()
	root := repoRoot()
	bin := filepath.Join(t.TempDir(), "github-activity-exporter")
	build := exec.Command("go", "build", "-o", bin, "./cmd/github-activity-exporter")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build exporter: %v\n%s", err, out)
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := fmt.Sprintf(`collection_interval: %s
export:
  interval: %s
  otlp:
    endpoint: %s
    protocol: http
  prometheus:
    enabled: false
health:
  listen_address: 127.0.0.1:%d
telemetry:
  enabled: true
  traces:
    enabled: false
  logs:
    enabled: false
  profiles:
    enabled: false
targets:
  - name: acme
    base_url: %s
    auth:
      token: e2e-token
    orgs:
      - org: acme
    renovate_dashboard:
      enabled: true
`, collectionInterval, testExportInterval, otlpURL, freePort(t), apiURL)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"CONFIG_FILE="+cfgPath,
		// Self-telemetry metrics inherit export.otlp whatever the caller's environment says.
		"OTEL_EXPORTER_OTLP_ENDPOINT=",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=",
		// go.memory.limit is reported only under a memory limit.
		"GOMEMLIMIT=512MiB",
	)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	logged := make(chan struct{})
	go func() {
		defer close(logged)
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			t.Log("exporter: " + sc.Text())
		}
		_, _ = io.Copy(io.Discard, pr) // keep draining if a line is too long for the scanner
	}()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start exporter: %v", err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		_ = pw.Close()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
		<-logged
	})
	return exited
}

// repoRoot returns the repository root, found from this file's location.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// freePort returns a currently free TCP port on 127.0.0.1.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// waitFor polls cond every 500ms until it returns true or the deadline passes.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}
