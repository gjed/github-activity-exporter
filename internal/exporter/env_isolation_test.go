package exporter_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric"
	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/gjed/github-activity-exporter/internal/exporter"
)

// selfTelemetryEnv sets every OTEL_EXPORTER_OTLP_* variable the SDK metric
// exporters read, other than the endpoint, to a value that would change the
// product push if it leaked through.
func selfTelemetryEnv(t *testing.T) {
	t.Helper()
	for _, prefix := range []string{"OTEL_EXPORTER_OTLP_", "OTEL_EXPORTER_OTLP_METRICS_"} {
		t.Setenv(prefix+"HEADERS", "x-leak=1")
		t.Setenv(prefix+"COMPRESSION", "gzip")
		t.Setenv(prefix+"TIMEOUT", "1") // 1 ms: every push would time out
		t.Setenv(prefix+"CERTIFICATE", "/nonexistent.pem")
		t.Setenv(prefix+"INSECURE", "false")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "delta")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_DEFAULT_HISTOGRAM_AGGREGATION", "base2_exponential_bucket_histogram")
	t.Setenv("OTEL_METRIC_EXPORT_TIMEOUT", "1")
}

// push records one counter and one histogram on p and flushes them.
func push(t *testing.T, p *exporter.Provider) {
	t.Helper()
	ctx := context.Background()
	m := p.Meter()
	ctr, _ := m.Int64Counter("test.counter")
	ctr.Add(ctx, 1)
	hist, _ := m.Float64Histogram("test.histogram", metric.WithUnit("s"))
	hist.Record(ctx, 1)
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
}

// checkPush asserts the request carries cumulative sums and explicit-bucket
// histograms.
func checkPush(t *testing.T, req *colmetricpb.ExportMetricsServiceRequest) {
	t.Helper()
	var sum, hist bool
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				switch d := m.GetData().(type) {
				case *metricpb.Metric_Sum:
					sum = true
					if d.Sum.GetAggregationTemporality() != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
						t.Errorf("%s temporality = %v, want cumulative", m.GetName(), d.Sum.GetAggregationTemporality())
					}
				case *metricpb.Metric_Histogram:
					hist = true
				case *metricpb.Metric_ExponentialHistogram:
					t.Errorf("%s is an exponential histogram", m.GetName())
				}
			}
		}
	}
	if !sum || !hist {
		t.Errorf("push lacks the counter or the histogram: %v", req)
	}
}

// TestProductPushIgnoresOTLPEnv_HTTP: OTEL_EXPORTER_OTLP_* configure
// self-telemetry and must not change the product OTLP/HTTP push.
func TestProductPushIgnoresOTLPEnv_HTTP(t *testing.T) {
	selfTelemetryEnv(t)

	var mu sync.Mutex
	var got []*http.Request
	var bodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond) // longer than the leaked 1 ms timeout
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got, bodies = append(got, r), append(bodies, b)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)

	p, err := exporter.New(context.Background(), exporter.Config{
		OTLPEndpoint:       srv.URL + "/v1/metrics",
		Headers:            nil,
		CollectionInterval: time.Hour,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	push(t, p)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("got %d requests, want 1", len(got))
	}
	if v := got[0].Header.Get("X-Leak"); v != "" {
		t.Errorf("env header reached the product push: x-leak=%s", v)
	}
	if v := got[0].Header.Get("Content-Encoding"); v != "" {
		t.Errorf("Content-Encoding = %q, want none", v)
	}
	var req colmetricpb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(bodies[0], &req); err != nil {
		t.Fatalf("decode push: %v", err)
	}
	checkPush(t, &req)
}

type metricsServer struct {
	colmetricpb.UnimplementedMetricsServiceServer
	mu   sync.Mutex
	md   []metadata.MD
	reqs []*colmetricpb.ExportMetricsServiceRequest
}

func (s *metricsServer) Export(ctx context.Context, req *colmetricpb.ExportMetricsServiceRequest) (*colmetricpb.ExportMetricsServiceResponse, error) {
	time.Sleep(20 * time.Millisecond) // longer than the leaked 1 ms timeout
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	s.md, s.reqs = append(s.md, md), append(s.reqs, req)
	s.mu.Unlock()
	return &colmetricpb.ExportMetricsServiceResponse{}, nil
}

// TestProductPushIgnoresOTLPEnv_GRPC is the OTLP/gRPC counterpart. The server
// sees decompressed requests, so compression is covered by the HTTP test only.
func TestProductPushIgnoresOTLPEnv_GRPC(t *testing.T) {
	selfTelemetryEnv(t)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ms := &metricsServer{}
	gs := grpc.NewServer()
	colmetricpb.RegisterMetricsServiceServer(gs, ms)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	p, err := exporter.New(context.Background(), exporter.Config{
		OTLPEndpoint:       lis.Addr().String(),
		Protocol:           exporter.ProtocolGRPC,
		TLSInsecure:        true,
		CollectionInterval: time.Hour,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	push(t, p)

	ms.mu.Lock()
	defer ms.mu.Unlock()
	if len(ms.reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(ms.reqs))
	}
	if v := ms.md[0].Get("x-leak"); len(v) != 0 {
		t.Errorf("env header reached the product push: x-leak=%v", v)
	}
	checkPush(t, ms.reqs[0])
}
