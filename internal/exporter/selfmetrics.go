package exporter

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	sc "github.com/gjed/github-activity-exporter/internal/semconv"
)

// SelfMetrics holds the instruments for exporter self-monitoring.
// Obtain one via NewSelfMetrics after the MeterProvider is initialised.
type SelfMetrics struct {
	scrapeDuration metric.Float64Histogram
	apiErrors      metric.Int64Counter
	otlpErrors     metric.Int64Counter
	rlRemaining    metric.Int64Gauge
	rlReset        metric.Int64Gauge
}

// NewSelfMetrics creates and registers all exporter self-metric instruments
// against the global MeterProvider (set by New).
func NewSelfMetrics() (*SelfMetrics, error) {
	meter := otel.GetMeterProvider().Meter("github.com/gjed/github-activity-exporter")

	scrapeDuration, err := meter.Float64Histogram(
		sc.MetricGitHubExporterScrapeDuration,
		metric.WithDescription("Duration of a complete collection cycle in seconds."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(
			0.1, 0.5, 1, 5, 10, 30, 60, 120, 300,
		),
	)
	if err != nil {
		return nil, err
	}

	apiErrors, err := meter.Int64Counter(
		sc.MetricGitHubExporterApiErrors,
		metric.WithDescription("Number of GitHub API errors encountered during collection."),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return nil, err
	}

	otlpErrors, err := meter.Int64Counter(
		sc.MetricGitHubExporterOTLPErrors,
		metric.WithDescription("Number of OTLP push failures."),
		metric.WithUnit("{error}"),
	)
	if err != nil {
		return nil, err
	}

	rlRemaining, err := meter.Int64Gauge(
		sc.MetricGitHubExporterApiRateLimitRemaining,
		metric.WithDescription("Number of GitHub API requests remaining in the current rate limit window."),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, err
	}

	rlReset, err := meter.Int64Gauge(
		sc.MetricGitHubExporterApiRateLimitReset,
		metric.WithDescription("Unix time, in seconds, at which the current GitHub API rate limit window resets."),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	return &SelfMetrics{
		scrapeDuration: scrapeDuration,
		apiErrors:      apiErrors,
		otlpErrors:     otlpErrors,
		rlRemaining:    rlRemaining,
		rlReset:        rlReset,
	}, nil
}

// RecordScrapeDuration records the wall-clock duration of a collection cycle.
// Call with the time the cycle started; it computes the elapsed duration.
func (s *SelfMetrics) RecordScrapeDuration(ctx context.Context, target string, start time.Time) {
	s.scrapeDuration.Record(ctx,
		time.Since(start).Seconds(),
		metric.WithAttributes(
			attribute.String(sc.AttrExporterTarget, target),
		),
	)
}

// RecordAPIError increments the API error counter.
// target is the org/repo slug (e.g. "my-org/my-repo"); endpoint is a
// github.api.endpoint value (e.g. sc.AttrGitHubApiEndpointGraphQL).
func (s *SelfMetrics) RecordAPIError(ctx context.Context, target, endpoint string) {
	s.apiErrors.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String(sc.AttrExporterTarget, target),
			attribute.String(sc.AttrGitHubApiEndpoint, endpoint),
		),
	)
}

// RecordOTLPError increments the OTLP push error counter.
func (s *SelfMetrics) RecordOTLPError(ctx context.Context, target string) {
	s.otlpErrors.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String(sc.AttrExporterTarget, target),
		),
	)
}

// RecordRateLimit records the target's GitHub API rate limit state: the
// requests remaining in the current window and the window reset as Unix time
// in seconds. A zero resetAt (no X-RateLimit-Reset header) leaves the reset
// gauge unchanged.
func (s *SelfMetrics) RecordRateLimit(ctx context.Context, target string, remaining int, resetAt time.Time) {
	attrs := metric.WithAttributes(attribute.String(sc.AttrExporterTarget, target))
	s.rlRemaining.Record(ctx, int64(remaining), attrs)
	if !resetAt.IsZero() {
		s.rlReset.Record(ctx, resetAt.Unix(), attrs)
	}
}
