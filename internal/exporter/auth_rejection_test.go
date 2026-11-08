package exporter_test

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	colmetricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/gjed/github-activity-exporter/internal/exporter"
)

const (
	authSecret  = "s3cret-token"
	authWarning = "OTLP endpoint rejected the first push as unauthorized"
)

// syncBuffer is a bytes.Buffer safe for the concurrent writes of slog.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// pushTwiceRejected pushes twice to an endpoint that rejects both pushes and
// returns the log output. Every request must carry the auth header.
func pushTwiceRejected(t *testing.T, cfg exporter.Config) string {
	t.Helper()
	var logs syncBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg.Headers = map[string]string{"authorization": "Bearer " + authSecret}
	cfg.CollectionInterval = time.Hour
	p, err := exporter.New(context.Background(), cfg, logger)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	push(t, p)
	push(t, p)
	return logs.String()
}

// checkAuthLogs asserts two push failures, one auth warning, and no header value.
func checkAuthLogs(t *testing.T, logs string) {
	t.Helper()
	if n := strings.Count(logs, "OTLP export failed"); n != 2 {
		t.Errorf("got %d export failure warnings, want 2:\n%s", n, logs)
	}
	if n := strings.Count(logs, authWarning); n != 1 {
		t.Errorf("got %d auth rejection warnings, want 1:\n%s", n, logs)
	}
	if strings.Contains(logs, authSecret) {
		t.Errorf("logs contain the header value:\n%s", logs)
	}
}

func TestAuthRejection_HTTP(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var mu sync.Mutex
			var auth []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				auth = append(auth, r.Header.Get("Authorization"))
				mu.Unlock()
				http.Error(w, http.StatusText(code), code)
			}))
			t.Cleanup(srv.Close)

			checkAuthLogs(t, pushTwiceRejected(t, exporter.Config{OTLPEndpoint: srv.URL + "/v1/metrics"}))

			mu.Lock()
			defer mu.Unlock()
			if len(auth) != 2 || auth[0] != "Bearer "+authSecret {
				t.Errorf("Authorization on requests = %q, want the configured header twice", auth)
			}
		})
	}
}

func TestAuthRejection_HTTPServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	logs := pushTwiceRejected(t, exporter.Config{OTLPEndpoint: srv.URL + "/v1/metrics"})
	if strings.Contains(logs, authWarning) {
		t.Errorf("auth warning logged for a 400:\n%s", logs)
	}
}

type rejectingServer struct {
	colmetricpb.UnimplementedMetricsServiceServer
	code codes.Code
}

func (s *rejectingServer) Export(context.Context, *colmetricpb.ExportMetricsServiceRequest) (*colmetricpb.ExportMetricsServiceResponse, error) {
	return nil, status.Error(s.code, "rejected")
}

func TestAuthRejection_GRPC(t *testing.T) {
	for _, code := range []codes.Code{codes.Unauthenticated, codes.PermissionDenied} {
		t.Run(code.String(), func(t *testing.T) {
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			gs := grpc.NewServer()
			colmetricpb.RegisterMetricsServiceServer(gs, &rejectingServer{code: code})
			go func() { _ = gs.Serve(lis) }()
			t.Cleanup(gs.Stop)

			checkAuthLogs(t, pushTwiceRejected(t, exporter.Config{
				OTLPEndpoint: lis.Addr().String(),
				Protocol:     exporter.ProtocolGRPC,
				TLSInsecure:  true,
			}))
		})
	}
}
