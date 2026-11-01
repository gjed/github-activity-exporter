## ADDED Requirements

### Requirement: OTLP push as primary export

The system SHALL push metrics to an OTLP-compatible endpoint after each collection cycle. OTLP is the required primary output — the exporter MUST NOT start without a configured OTLP endpoint.

#### Scenario: Metrics pushed after each collection cycle

- **WHEN** a collection cycle completes
- **THEN** all collected metrics are pushed to the configured OTLP endpoint within the same cycle

#### Scenario: OTLP/HTTP supported

- **WHEN** `export.otlp.protocol: http` is configured
- **THEN** metrics are sent over OTLP/HTTP (protobuf) to the configured endpoint

#### Scenario: OTLP/gRPC supported

- **WHEN** `export.otlp.protocol: grpc` is configured
- **THEN** metrics are sent over OTLP/gRPC to the configured endpoint

#### Scenario: Default protocol is HTTP

- **WHEN** `export.otlp.protocol` is not configured
- **THEN** OTLP/HTTP is used

#### Scenario: OTLP push failure does not crash exporter

- **WHEN** the OTLP endpoint is unreachable
- **THEN** the exporter logs a warning, increments `github_exporter.otlp.errors` counter, and continues to the next collection cycle

### Requirement: Prometheus bridge as optional secondary

The system SHALL optionally expose a `/metrics` HTTP endpoint in Prometheus text exposition format, implemented via the OTel Prometheus bridge exporter (not a separate metric registry).

#### Scenario: Prometheus endpoint disabled by default

- **WHEN** `export.prometheus.enabled` is not set or is `false`
- **THEN** no HTTP server for `/metrics` is started

#### Scenario: Prometheus endpoint enabled

- **WHEN** `export.prometheus.enabled: true` is configured
- **THEN** a GET request to `/metrics` returns HTTP 200 with Prometheus text format exposing the same metrics as the OTLP output

#### Scenario: Prometheus bridge reads same metric state as OTLP

- **WHEN** both OTLP and Prometheus outputs are enabled
- **THEN** both expose identical metric values (no separate collection; one MeterProvider, two exporters)

### Requirement: Configurable collection interval

The system SHALL support a configurable interval for how often metrics are collected from the GitHub API and pushed via OTLP.

#### Scenario: Default collection interval

- **WHEN** `collection_interval` is not configured
- **THEN** metrics are collected and pushed every 5 minutes

#### Scenario: Custom interval respected

- **WHEN** `collection_interval: 15m` is set
- **THEN** collection and OTLP push occur approximately every 15 minutes

### Requirement: Exporter self-metrics

The system SHALL emit metrics about its own operation using the same OTel MeterProvider and OTLP pipeline.

#### Scenario: Scrape duration recorded

- **WHEN** a collection cycle completes
- **THEN** `github_exporter.scrape.duration` histogram is updated with the cycle duration in seconds

#### Scenario: API error counter incremented

- **WHEN** a GitHub API call returns an error
- **THEN** `github_exporter.api.errors` counter is incremented with `target` and `endpoint` attributes

#### Scenario: OTLP error counter incremented

- **WHEN** an OTLP push fails
- **THEN** `github_exporter.otlp.errors` counter is incremented

### Requirement: Configurable OTLP headers and TLS

The system SHALL support configuring arbitrary HTTP headers (e.g., `Authorization`) and TLS settings for the OTLP endpoint.

#### Scenario: Auth header injected

- **WHEN** `export.otlp.headers: {Authorization: "Bearer <token>"}` is configured
- **THEN** all OTLP push requests include that header
