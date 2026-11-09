package exporter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/gjed/github-activity-exporter/internal/exporter"
)

// stubProducer returns one gauge named name, or err.
type stubProducer struct {
	name string
	err  error
}

func (s stubProducer) Produce(context.Context) ([]metricdata.ScopeMetrics, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []metricdata.ScopeMetrics{{
		Scope: instrumentation.Scope{Name: "stub"},
		Metrics: []metricdata.Metrics{{
			Name: s.name,
			Data: metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{Time: time.Now(), Value: 1}}},
		}},
	}}, nil
}

// TestAttachProducer: attached metrics travel in the product push under one
// resource; a failing producer is logged and the push still carries the
// product metrics.
func TestAttachProducer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		producer stubProducer
		attached bool
	}{
		{"attached metrics pushed", stubProducer{name: "test.attached"}, true},
		{"failing producer does not block the push", stubProducer{err: errors.New("boom")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink, endpoint := newOTLPSink(t)
			ctx := context.Background()
			p, err := exporter.New(ctx, exporter.Config{OTLPEndpoint: endpoint, ExportInterval: time.Hour}, nil)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			p.AttachProducer(tc.producer)
			ctr, _ := p.Meter().Int64Counter("test.product")
			ctr.Add(ctx, 1)

			if err := p.ForceFlush(ctx); err != nil {
				t.Fatalf("ForceFlush: %v", err)
			}
			reqs := sink.requests()
			if len(reqs) != 1 {
				t.Fatalf("got %d pushes, want 1", len(reqs))
			}
			req := reqs[0]
			if n := len(req.GetResourceMetrics()); n != 1 {
				t.Errorf("push carries %d resources, want 1", n)
			}
			if findMetric(req, "test.product") == nil {
				t.Error("push lacks the product metric")
			}
			if got := findMetric(req, "test.attached") != nil; got != tc.attached {
				t.Errorf("attached metric in push = %v, want %v", got, tc.attached)
			}
		})
	}
}
