package exporter_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/gjed/renovate-exporter/internal/exporter"
)

func TestNew_MissingEndpoint(t *testing.T) {
	_, err := exporter.New(context.Background(), exporter.Config{}, nil)
	if err == nil {
		t.Fatal("expected error for missing OTLPEndpoint, got nil")
	}
}

func TestNew_DefaultsProtocol(t *testing.T) {
	// Use a non-existent endpoint; we only test the Provider is created, not
	// that export succeeds.
	cfg := exporter.Config{
		OTLPEndpoint:       "http://localhost:14318", // won't be dialled at startup
		CollectionInterval: 10 * time.Minute,         // long so no push happens in test
	}
	p, err := exporter.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	})

	if p.MeterProvider() == nil {
		t.Fatal("MeterProvider is nil")
	}
}

func TestNew_SetsGlobalMeterProvider(t *testing.T) {
	cfg := exporter.Config{
		OTLPEndpoint:       "http://localhost:14318",
		CollectionInterval: 10 * time.Minute,
	}
	p, err := exporter.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	})

	// Verify the global provider is exactly the one we constructed, not the
	// default noop provider (which is also non-nil).
	if otel.GetMeterProvider() != p.MeterProvider() {
		t.Fatal("global MeterProvider is not the provider returned by New()")
	}
}

func TestNew_PrometheusEnabled(t *testing.T) {
	const addr = "127.0.0.1:19091"
	cfg := exporter.Config{
		OTLPEndpoint:       "http://localhost:14318",
		CollectionInterval: 10 * time.Minute,
		PrometheusEnabled:  true,
		PrometheusAddr:     addr,
	}
	p, err := exporter.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	})

	// Give the server goroutine a moment to bind.
	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestNew_GRPCProtocol(t *testing.T) {
	cfg := exporter.Config{
		OTLPEndpoint:       "localhost:14317",
		Protocol:           exporter.ProtocolGRPC,
		CollectionInterval: 10 * time.Minute,
		TLSInsecure:        true,
	}
	p, err := exporter.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	})

	if p.MeterProvider() == nil {
		t.Fatal("MeterProvider is nil for gRPC config")
	}
}

func TestSelfMetrics_RecordScrapeDuration(t *testing.T) {
	cfg := exporter.Config{
		OTLPEndpoint:       "http://localhost:14318",
		CollectionInterval: 10 * time.Minute,
	}
	p, err := exporter.New(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	})

	sm, err := exporter.NewSelfMetrics()
	if err != nil {
		t.Fatalf("NewSelfMetrics: %v", err)
	}

	// Should not panic
	sm.RecordScrapeDuration(context.Background(), "test-org/test-repo", time.Now().Add(-100*time.Millisecond))
	sm.RecordAPIError(context.Background(), "test-org/test-repo")
	sm.RecordOTLPError(context.Background(), "test-org/test-repo")
}
