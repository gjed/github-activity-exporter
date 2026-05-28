package exporter

import "time"

// Protocol selects the OTLP transport.
type Protocol string

const (
	// ProtocolHTTP selects OTLP/HTTP transport.
	ProtocolHTTP Protocol = "http"
	// ProtocolGRPC selects OTLP/gRPC transport.
	ProtocolGRPC Protocol = "grpc"
)

// Config holds all configuration for the metric export pipeline.
type Config struct {
	// OTLP endpoint URL (required).
	// For HTTP: "https://host:4318"
	// For gRPC: "host:4317"
	OTLPEndpoint string

	// Protocol selects OTLP/HTTP (default) or OTLP/gRPC.
	Protocol Protocol

	// Headers are injected into every OTLP push request (e.g. Authorization).
	Headers map[string]string

	// TLSInsecure disables TLS certificate verification. Do not use in production.
	TLSInsecure bool

	// CollectionInterval is how often metrics are pushed via OTLP.
	// Defaults to 5 minutes.
	CollectionInterval time.Duration

	// PrometheusEnabled enables the optional /metrics Prometheus bridge.
	PrometheusEnabled bool

	// PrometheusAddr is the listen address for the /metrics HTTP server.
	// Defaults to ":9090".
	PrometheusAddr string

	// Target is the exporter.target attribute value used in self-metrics
	// (e.g. "my-org/my-repo"). Defaults to "default".
	Target string
}

func (c *Config) defaults() {
	if c.Protocol == "" {
		c.Protocol = ProtocolHTTP
	}
	if c.CollectionInterval == 0 {
		c.CollectionInterval = 5 * time.Minute
	}
	if c.PrometheusAddr == "" {
		c.PrometheusAddr = ":9090"
	}
	if c.Target == "" {
		c.Target = "default"
	}
}
