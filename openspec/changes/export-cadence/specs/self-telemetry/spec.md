## MODIFIED Requirements

### Requirement: Go runtime metrics

When self-telemetry metrics are active, the exporter SHALL record the Go runtime metrics of the OTel semantic conventions on the self-telemetry MeterProvider, pushed over OTLP with the other self-telemetry metrics and exposed on `/metrics` when the Prometheus bridge is enabled. When self-telemetry metrics are inactive, no `go.*` metric SHALL be exported on OTLP or exposed on `/metrics`.

#### Scenario: Runtime metrics with the signal on

- **WHEN** `telemetry.enabled` and `telemetry.metrics.enabled` are true and the Prometheus bridge is enabled
- **THEN** the OTLP self-telemetry push and `/metrics` carry `go.memory.used`, `go.memory.allocated`, `go.memory.allocations`, `go.memory.gc.goal`, `go.goroutine.count`, `go.processor.limit`, `go.config.gogc` and `go.schedule.duration`

#### Scenario: Memory limit only when set

- **WHEN** self-telemetry metrics are active and `GOMEMLIMIT` is not set
- **THEN** `go.memory.limit` is not reported; with `GOMEMLIMIT=256MiB` it is reported as 268435456

#### Scenario: Runtime metrics with the signal off

- **WHEN** the exporter runs with the default config, or with `telemetry.metrics.enabled: false`, or with `OTEL_METRICS_EXPORTER=none`
- **THEN** neither the OTLP push nor `/metrics` carries any `go.*` metric

## ADDED Requirements

### Requirement: Inherited self-telemetry metrics in the product push

When self-telemetry metrics are active and neither `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` nor `OTEL_EXPORTER_OTLP_ENDPOINT` is set, they SHALL travel in the product OTLP push, every `export.interval` and after each cycle, with one resource and one `target_info`. `OTEL_METRIC_EXPORT_INTERVAL` SHALL NOT apply then. With an env endpoint they SHALL keep their own OTLP push.

#### Scenario: One push, one target_info

- **WHEN** self-telemetry metrics are active, no OTLP endpoint env var is set, and the exporter pushes to a stock Prometheus 3.x without an out-of-order window
- **THEN** `go.*` and `http.*` metrics arrive in the same requests as the GitHub metrics, `target_info` samples have strictly increasing timestamps, and Prometheus rejects no sample as out of order

#### Scenario: Export interval variable ignored when inherited

- **WHEN** self-telemetry metrics inherit `export.otlp` and `OTEL_METRIC_EXPORT_INTERVAL=5000` is set
- **THEN** self-telemetry metrics are pushed on the `export.interval` cadence, not every 5 seconds

#### Scenario: Env endpoint keeps a separate push

- **WHEN** self-telemetry metrics are active and `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` is set
- **THEN** self-telemetry metrics are pushed to that endpoint every `OTEL_METRIC_EXPORT_INTERVAL` (default 60 s), and the product push carries no `go.*` or `http.*` metric

#### Scenario: Self-telemetry failure does not block the product push

- **WHEN** collecting the inherited self-telemetry metrics fails
- **THEN** the product push still carries the GitHub metrics and the error is logged
