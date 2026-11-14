//go:build e2e

package e2e

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

// OTLP logs (https://github.com/gjed/github-activity-exporter/issues/76):
// the built binary with self-telemetry logs on, an in-process OTLP receiver
// and the mock GitHub. No Docker.

// otlpSink is an OTLP/HTTP receiver. It keeps the body of every accepted
// request by path and, while reject is set, answers 400, which the exporters
// do not retry, to every request but traces: the product metrics push
// (export.otlp.endpoint as given) and the self-telemetry logs.
type otlpSink struct {
	*httptest.Server
	reject atomic.Bool
	mu     sync.Mutex
	bodies map[string][][]byte
}

func newOTLPSink(t *testing.T) *otlpSink {
	t.Helper()
	s := &otlpSink{bodies: map[string][][]byte{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if s.reject.Load() && r.URL.Path != "/v1/traces" {
			http.Error(w, "rejected by the e2e sink", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.bodies[r.URL.Path] = append(s.bodies[r.URL.Path], body)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *otlpSink) received(path string) [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.bodies[path]...)
}

// logRecords decodes every accepted /v1/logs request.
func (s *otlpSink) logRecords(t *testing.T) []*logspb.LogRecord {
	t.Helper()
	var out []*logspb.LogRecord
	for _, b := range s.received("/v1/logs") {
		var req collogs.ExportLogsServiceRequest
		if err := proto.Unmarshal(b, &req); err != nil {
			t.Fatalf("decode /v1/logs: %v", err)
		}
		for _, rl := range req.GetResourceLogs() {
			for _, sl := range rl.GetScopeLogs() {
				out = append(out, sl.GetLogRecords()...)
			}
		}
	}
	return out
}

// stopExporter sends SIGTERM, which flushes self-telemetry, and waits for
// the process to exit.
func stopExporter(t *testing.T, run *exporterRun) {
	t.Helper()
	if err := run.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}
	select {
	case <-run.exited:
	case <-time.After(30 * time.Second):
		t.Fatal("exporter did not exit after SIGTERM")
	}
}

// waitReady waits for /readyz: every target has completed a cycle.
func waitReady(t *testing.T, run *exporterRun, healthAddr string) {
	t.Helper()
	ok := waitFor(30*time.Second, func() bool {
		resp, err := http.Get("http://" + healthAddr + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	if !ok {
		t.Fatalf("exporter not ready\n%s", run.output())
	}
}

// No credential reaches stdout or the OTLP logs: a PAT target, a GitHub App
// target and OTLP headers from export.otlp.headers and headers_env, each with
// a planted value, through a full cycle with logs and traces on.
func TestLogs_NoCredentials(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	keyPath := filepath.Join(t.TempDir(), "app.pem")
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	const (
		pat         = "ghp_PLANTEDpat7f3a9c1e5b"
		installTok  = "ghs_PLANTEDinstall2d8e4b6a"
		fileHeader  = "PLANTEDfileheader91c3e7"
		envHeader   = "PLANTEDenvheader5a2f8d"
		headersEnvN = "E2E_OTLP_HEADERS"
	)
	secrets := map[string]string{
		"PAT":                       pat,
		"installation token":        installTok,
		"export.otlp.headers value": fileHeader,
		"headers_env value":         envHeader,
	}
	for i, line := range strings.Split(strings.TrimSpace(string(keyPEM)), "\n") {
		if !strings.HasPrefix(line, "-----") {
			secrets[fmt.Sprintf("App private key line %d", i)] = line
		}
	}

	mock := newMockGitHub(t)
	mock.InstallationToken = installTok
	sink := newOTLPSink(t)
	healthAddr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	apiURL := mock.URL + "/api/v3"
	run := runExporter(t, fmt.Sprintf(`collection_interval: 1m
export:
  interval: %s
  otlp:
    endpoint: %s
    protocol: http
    headers:
      authorization: Bearer %s
    headers_env: %s
  prometheus:
    enabled: false
health:
  listen_address: %s
telemetry:
  enabled: true
  profiles:
    enabled: false
targets:
  - name: pat
    base_url: %s
    auth:
      token: %s
    orgs:
      - org: acme
  - name: app
    base_url: %s
    auth:
      app:
        app_id: 1
        installation_id: 2
        private_key_path: %s
    orgs:
      - org: acme
`, testExportInterval, sink.URL, fileHeader, headersEnvN, healthAddr, apiURL, pat, apiURL, keyPath),
		headersEnvN+"=x-scope-orgid="+envHeader,
		"OTEL_BLRP_SCHEDULE_DELAY=200",
	)
	waitReady(t, run, healthAddr)
	stopExporter(t, run)

	stdout := run.output()
	logs := sink.received("/v1/logs")
	records := sink.logRecords(t)
	if len(records) == 0 {
		t.Fatalf("no OTLP log record received\n%s", stdout)
	}
	var correlated bool
	for _, r := range records {
		if len(r.GetTraceId()) > 0 && strings.Contains(stdout, `"trace_id":"`+hex.EncodeToString(r.GetTraceId())+`"`) {
			correlated = true
		}
	}
	if !correlated {
		t.Error("no OTLP log record whose trace_id is also on a stdout line")
	}
	for name, v := range secrets {
		if strings.Contains(stdout, v) {
			t.Errorf("stdout contains the %s", name)
		}
		for _, b := range logs {
			if bytes.Contains(b, []byte(v)) {
				t.Errorf("an OTLP log payload contains the %s", name)
				break
			}
		}
	}
	if u := mock.unhandledRequests(); len(u) > 0 {
		t.Errorf("unhandled mock requests: %v", u)
	}
}

// The OTLP exporters' own errors stay on stdout: with log and metric
// exports rejected, then accepted, no export error reaches the OTLP logs.
func TestLogs_ExportErrorsStayOnStdout(t *testing.T) {
	mock := newMockGitHub(t)
	sink := newOTLPSink(t)
	sink.reject.Store(true)
	healthAddr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	run := runExporter(t, fmt.Sprintf(`collection_interval: 1m
export:
  interval: %s
  otlp:
    endpoint: %s
    protocol: http
  prometheus:
    enabled: false
health:
  listen_address: %s
telemetry:
  enabled: true
  profiles:
    enabled: false
targets:
  - name: acme
    base_url: %s
    auth:
      token: e2e-token
    orgs:
      - org: acme
`, testExportInterval, sink.URL, healthAddr, mock.URL+"/api/v3"),
		"OTEL_BLRP_SCHEDULE_DELAY=200",
	)
	exportErrors := []string{"opentelemetry error", "OTLP export failed — will retry next cycle"}
	ok := waitFor(30*time.Second, func() bool {
		out := run.output()
		return strings.Contains(out, `"msg":"`+exportErrors[0]+`"`) && strings.Contains(out, `"msg":"`+exportErrors[1]+`"`)
	})
	if !ok {
		t.Fatalf("export errors not on stdout\n%s", run.output())
	}
	// The shutdown lines go out in the final, accepted, log export, after
	// any export error a bridge would have queued.
	sink.reject.Store(false)
	stopExporter(t, run)

	records := sink.logRecords(t)
	if len(records) == 0 {
		t.Fatalf("no OTLP log record received after the rejections\n%s", run.output())
	}
	for _, r := range records {
		for _, e := range exportErrors {
			if r.GetBody().GetStringValue() == e {
				t.Errorf("export error %q exported as an OTLP log record", e)
			}
		}
	}
}
