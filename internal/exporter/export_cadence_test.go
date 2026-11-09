package exporter_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric"
	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"

	"github.com/gjed/github-activity-exporter/internal/exporter"
)

// otlpSink is an OTLP/HTTP metrics endpoint that keeps every decoded request.
type otlpSink struct {
	mu   sync.Mutex
	reqs []*colmetricpb.ExportMetricsServiceRequest
}

func newOTLPSink(t *testing.T) (*otlpSink, string) {
	t.Helper()
	s := &otlpSink{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		req := &colmetricpb.ExportMetricsServiceRequest{}
		if err := proto.Unmarshal(b, req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.reqs = append(s.reqs, req)
		s.mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	return s, srv.URL + "/v1/metrics"
}

func (s *otlpSink) requests() []*colmetricpb.ExportMetricsServiceRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*colmetricpb.ExportMetricsServiceRequest(nil), s.reqs...)
}

// findMetric returns the named metric of a request, or nil.
func findMetric(req *colmetricpb.ExportMetricsServiceRequest, name string) *metricpb.Metric {
	for _, rm := range req.GetResourceMetrics() {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				if m.GetName() == name {
					return m
				}
			}
		}
	}
	return nil
}

// TestExportInterval_RepushesCumulative: the product reader pushes every
// ExportInterval without any flush, and each push of a counter and histogram
// recorded once carries the same cumulative value and start time, so repeated
// pushes never double-count.
func TestExportInterval_RepushesCumulative(t *testing.T) {
	sink, endpoint := newOTLPSink(t)
	ctx := context.Background()
	p, err := exporter.New(ctx, exporter.Config{
		OTLPEndpoint:   endpoint,
		ExportInterval: 50 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	m := p.Meter()
	ctr, _ := m.Int64Counter("test.counter")
	hist, _ := m.Float64Histogram("test.histogram", metric.WithUnit("s"))
	ctr.Add(ctx, 1)
	hist.Record(ctx, 1)

	const want = 4
	deadline := time.Now().Add(5 * time.Second)
	for len(sink.requests()) < want {
		if time.Now().After(deadline) {
			t.Fatalf("got %d pushes in 5s with ExportInterval 50ms, want %d", len(sink.requests()), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A per-cycle flush on top of the periodic pushes must not change the values either.
	if err := p.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	reqs := sink.requests()
	var ctrStart, histStart uint64
	for i, req := range reqs {
		c := findMetric(req, "test.counter").GetSum()
		h := findMetric(req, "test.histogram").GetHistogram()
		if c == nil || h == nil || len(c.GetDataPoints()) != 1 || len(h.GetDataPoints()) != 1 {
			t.Fatalf("push %d lacks the counter or histogram point: %v", i, req)
		}
		if c.GetAggregationTemporality() != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE ||
			h.GetAggregationTemporality() != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
			t.Fatalf("push %d: temporality not cumulative", i)
		}
		cdp, hdp := c.GetDataPoints()[0], h.GetDataPoints()[0]
		if cdp.GetAsInt() != 1 {
			t.Errorf("push %d: counter = %d, want 1", i, cdp.GetAsInt())
		}
		if hdp.GetCount() != 1 || hdp.GetSum() != 1 {
			t.Errorf("push %d: histogram count = %d sum = %v, want 1 and 1", i, hdp.GetCount(), hdp.GetSum())
		}
		if i == 0 {
			ctrStart, histStart = cdp.GetStartTimeUnixNano(), hdp.GetStartTimeUnixNano()
		} else if cdp.GetStartTimeUnixNano() != ctrStart || hdp.GetStartTimeUnixNano() != histStart {
			t.Errorf("push %d: start time changed", i)
		}
	}
	t.Logf("%d pushes, counter 1 and histogram count 1 in each", len(reqs))
}
