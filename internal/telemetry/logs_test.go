package telemetry_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"

	"github.com/gjed/github-activity-exporter/internal/config"
)

// logReceiver is an OTLP/HTTP server that decodes the log records it receives.
type logReceiver struct {
	*httptest.Server
	mu      sync.Mutex
	records []*logspb.LogRecord
}

func newLogReceiver(t *testing.T) *logReceiver {
	t.Helper()
	r := &logReceiver{}
	r.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		if req.URL.Path == "/v1/logs" {
			var msg collogs.ExportLogsServiceRequest
			if err := proto.Unmarshal(body, &msg); err != nil {
				t.Errorf("decode logs: %v", err)
			}
			r.mu.Lock()
			for _, rl := range msg.GetResourceLogs() {
				for _, sl := range rl.GetScopeLogs() {
					r.records = append(r.records, sl.GetLogRecords()...)
				}
			}
			r.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(r.Close)
	return r
}

// record returns the received record with body msg, or nil.
func (r *logReceiver) record(msg string) *logspb.LogRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.records {
		if rec.GetBody().GetStringValue() == msg {
			return rec
		}
	}
	return nil
}

// stdoutLines decodes JSON lines by message.
func stdoutLines(t *testing.T, out *bytes.Buffer) map[string]map[string]any {
	t.Helper()
	lines := map[string]map[string]any{}
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout line %q: %v", l, err)
		}
		lines[m["msg"].(string)] = m
	}
	return lines
}

func TestLogger_InactiveIsStdoutHandler(t *testing.T) {
	clearEnv(t, nil)
	rcv := newReceiver(t)
	tel := newTelemetry(t, toggles(true, true, true, false, false), config.OTLP{Endpoint: rcv.URL, Protocol: "http"}, nil, nil)
	h := slog.NewJSONHandler(io.Discard, nil)
	if got := tel.Logger(h, slog.LevelInfo).Handler(); got != h {
		t.Fatalf("Logger handler = %T, want the stdout handler itself", got)
	}
	emitAll(t, tel)
	if rcv.request("/v1/logs") != nil {
		t.Error("logs exported with the signal off")
	}
}

func TestLogger_Active(t *testing.T) {
	clearEnv(t, nil)
	rcv := newLogReceiver(t)
	tel := newTelemetry(t, toggles(true, false, false, true, false), config.OTLP{Endpoint: rcv.URL, Protocol: "http"}, nil, nil)
	var out bytes.Buffer
	logger := tel.Logger(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelInfo}), slog.LevelInfo)

	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("test").Start(context.Background(), "unit")
	sc := span.SpanContext()
	logger.DebugContext(ctx, "debug in span")
	logger.InfoContext(ctx, "info in span", "exporter.target", "prod")
	logger.WarnContext(ctx, "warn in span")
	logger.ErrorContext(ctx, "error in span")
	span.End()
	logger.InfoContext(context.Background(), "outside")
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if rcv.record("debug in span") != nil {
		t.Error("debug record exported below the info level")
	}
	for msg, want := range map[string]struct {
		num  logspb.SeverityNumber
		text string
	}{
		"info in span":  {logspb.SeverityNumber_SEVERITY_NUMBER_INFO, "INFO"},
		"warn in span":  {logspb.SeverityNumber_SEVERITY_NUMBER_WARN, "WARN"},
		"error in span": {logspb.SeverityNumber_SEVERITY_NUMBER_ERROR, "ERROR"},
	} {
		rec := rcv.record(msg)
		if rec == nil {
			t.Errorf("%q not exported", msg)
			continue
		}
		if rec.GetSeverityNumber() != want.num || rec.GetSeverityText() != want.text {
			t.Errorf("%q: severity %v %q, want %v %q", msg, rec.GetSeverityNumber(), rec.GetSeverityText(), want.num, want.text)
		}
		if hex.EncodeToString(rec.GetTraceId()) != sc.TraceID().String() || hex.EncodeToString(rec.GetSpanId()) != sc.SpanID().String() {
			t.Errorf("%q: trace %x span %x, want %s %s", msg, rec.GetTraceId(), rec.GetSpanId(), sc.TraceID(), sc.SpanID())
		}
	}
	if rec := rcv.record("info in span"); rec != nil {
		if a := rec.GetAttributes(); len(a) != 1 || a[0].GetKey() != "exporter.target" || a[0].GetValue().GetStringValue() != "prod" {
			t.Errorf("attributes = %v, want exporter.target=prod", a)
		}
	}
	if rec := rcv.record("outside"); rec == nil {
		t.Error("record outside a span not exported")
	} else if len(rec.GetTraceId()) != 0 || len(rec.GetSpanId()) != 0 {
		t.Errorf("record outside a span has trace %x span %x", rec.GetTraceId(), rec.GetSpanId())
	}

	lines := stdoutLines(t, &out)
	if _, ok := lines["debug in span"]; ok {
		t.Error("debug line on stdout")
	}
	for _, msg := range []string{"info in span", "warn in span", "error in span"} {
		l := lines[msg]
		if l["trace_id"] != sc.TraceID().String() || l["span_id"] != sc.SpanID().String() {
			t.Errorf("stdout %q: trace_id %v span_id %v, want %s %s", msg, l["trace_id"], l["span_id"], sc.TraceID(), sc.SpanID())
		}
	}
	if l := lines["outside"]; l == nil {
		t.Error("outside line missing on stdout")
	} else if _, ok := l["trace_id"]; ok {
		t.Error("stdout line outside a span has trace_id")
	}
}
