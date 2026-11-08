package exporter

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sc "github.com/gjed/github-activity-exporter/internal/semconv"
	"github.com/gjed/github-activity-exporter/internal/telemetry"
)

// errorCountingExporter wraps an OTLP metric.Exporter. When Export returns an
// error it logs a warning and increments the github_exporter.otlp.errors
// counter, then returns nil so the SDK never disables the reader (non-fatal).
type errorCountingExporter struct {
	inner   sdkmetric.Exporter
	logger  *slog.Logger
	target  string // exporter.target label value
	version string // instrumentation scope version

	once       sync.Once
	otlpErrors metric.Int64Counter // initialised lazily on first export
	firstPush  sync.Once
}

var _ sdkmetric.Exporter = (*errorCountingExporter)(nil)

func (e *errorCountingExporter) initCounter() {
	e.once.Do(func() {
		meter := otel.GetMeterProvider().Meter(telemetry.ScopeName, metric.WithInstrumentationVersion(e.version))
		ctr, err := meter.Int64Counter(
			sc.MetricGitHubExporterOTLPErrors,
			metric.WithDescription("Number of OTLP push failures."),
			metric.WithUnit("{error}"),
		)
		if err != nil {
			e.logger.Warn("failed to create otlp.errors counter", "err", err)
			return
		}
		e.otlpErrors = ctr
	})
}

func (e *errorCountingExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	err := e.inner.Export(ctx, rm)
	e.firstPush.Do(func() {
		if err != nil && isAuthRejection(err) {
			e.logger.Warn("OTLP endpoint rejected the first push as unauthorized; check export.otlp.headers and export.otlp.headers_env",
				"target", e.target)
		}
	})
	if err != nil {
		e.logger.Warn("OTLP export failed — will retry next cycle", "err", err)
		e.initCounter()
		if e.otlpErrors != nil {
			e.otlpErrors.Add(ctx, 1,
				metric.WithAttributes(
					attribute.String(sc.AttrExporterTarget, e.target),
				),
			)
		}
	}
	return nil // always nil — keeps the reader alive
}

// isAuthRejection reports whether err is an OTLP push rejected as
// unauthenticated or forbidden.
// SIMPLIFIED: otlpmetrichttp reports HTTP failures as plain text carrying
// resp.Status, with no typed status code, so HTTP is matched on that text.
// Upgrade path: a typed status error, if the SDK ever exposes one.
func isAuthRejection(err error) bool {
	switch status.Code(err) {
	case codes.Unauthenticated, codes.PermissionDenied:
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, " 401 Unauthorized") || strings.Contains(msg, " 403 Forbidden")
}

func (e *errorCountingExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.inner.Temporality(k)
}

func (e *errorCountingExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.inner.Aggregation(k)
}

func (e *errorCountingExporter) ForceFlush(ctx context.Context) error {
	return e.inner.ForceFlush(ctx)
}

func (e *errorCountingExporter) Shutdown(ctx context.Context) error {
	return e.inner.Shutdown(ctx)
}
