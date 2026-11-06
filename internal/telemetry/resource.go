// Package telemetry owns what every OTel provider in the exporter shares (the
// resource and the instrumentation scope) and the optional self-telemetry
// providers: telemetry about the exporter itself, separate from the GitHub
// metrics it exports.
package telemetry

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// ScopeName is the instrumentation scope of every meter, tracer and logger.
const ScopeName = "github.com/gjed/github-activity-exporter"

// ServiceName is the default service.name.
const ServiceName = "github-activity-exporter"

// NewResource builds the resource shared by the product MeterProvider and the
// self-telemetry providers. Later sources win: the defaults (service.name,
// service.version = version, a UUIDv4 service.instance.id), then the SDK
// detectors, then OTEL_RESOURCE_ATTRIBUTES and OTEL_SERVICE_NAME.
func NewResource(ctx context.Context, version string) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(ServiceName),
			semconv.ServiceVersion(version),
			semconv.ServiceInstanceID(uuid.NewString()),
		),
		resource.WithProcess(),
		resource.WithOS(),
		resource.WithContainer(),
		resource.WithHost(),
		resource.WithFromEnv(),
	)
}
