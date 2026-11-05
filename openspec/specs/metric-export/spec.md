# metric-export Specification

## Purpose

Export metrics through an OTel MeterProvider with OTLP (HTTP or gRPC) as the primary output, an optional Prometheus /metrics bridge, and exporter self-metrics.

## Requirements

### Requirement: OTLP push as primary export

The system SHALL push metrics to an OTLP-compatible endpoint after each collection cycle. OTLP is the required primary output — the exporter MUST NOT start without a configured OTLP endpoint.

#### Scenario: Metrics pushed after each collection cycle

- **WHEN** a collection cycle completes
- **THEN** all collected metrics are pushed to the configured OTLP endpoint within the same cycle

#### Scenario: Missing endpoint is fatal

- **WHEN** `export.otlp.endpoint` is not configured or is empty
- **THEN** the exporter logs an error naming `export.otlp.endpoint` and exits with a non-zero status before collecting anything

#### Scenario: Malformed endpoint is fatal

- **WHEN** `export.otlp.endpoint` is not an `http://` or `https://` URL with a host for protocol `http`, or contains a scheme for protocol `grpc`
- **THEN** the exporter logs an error naming `export.otlp.endpoint` and exits with a non-zero status

#### Scenario: OTLP/HTTP supported

- **WHEN** `export.otlp.protocol: http` is configured
- **THEN** metrics are sent over OTLP/HTTP (protobuf) to the configured endpoint

#### Scenario: OTLP/gRPC supported

- **WHEN** `export.otlp.protocol: grpc` is configured
- **THEN** metrics are sent over OTLP/gRPC to the configured endpoint

#### Scenario: Default protocol is HTTP

- **WHEN** `export.otlp.protocol` is not configured
- **THEN** OTLP/HTTP is used

#### Scenario: Unknown protocol is fatal

- **WHEN** `export.otlp.protocol` is set to a value other than `http` or `grpc`
- **THEN** the exporter logs an error naming `export.otlp.protocol` and exits with a non-zero status

#### Scenario: OTLP push failure does not crash exporter

- **WHEN** the OTLP endpoint is unreachable
- **THEN** the exporter logs a warning, increments `github_exporter.otlp.errors` counter, and continues to the next collection cycle

### Requirement: Prometheus bridge as optional secondary

The system SHALL optionally expose a `/metrics` HTTP endpoint in Prometheus text exposition format, implemented via the OTel Prometheus bridge exporter (not a separate metric registry). The bridge SHALL expose metric names per the registry naming contract: each metric family name equals the registry metric name exactly, with no suffix and no escaping, and histogram samples add only `_bucket`, `_sum` and `_count`. The endpoint SHALL NOT serve the OpenMetrics text format, which requires a `_total` suffix on counter names; a scraper that asks for OpenMetrics first SHALL receive Prometheus text format with counters typed `counter`.

#### Scenario: Prometheus endpoint disabled by default

- **WHEN** `export.prometheus.enabled` is not set or is `false`
- **THEN** no HTTP server for `/metrics` is started

#### Scenario: Prometheus endpoint enabled

- **WHEN** `export.prometheus.enabled: true` is configured
- **THEN** a GET request to `/metrics` returns HTTP 200 with Prometheus text format exposing the same metrics as the OTLP output

#### Scenario: Default listen address

- **WHEN** `export.prometheus.enabled: true` is configured and `export.prometheus.listen_address` is not set
- **THEN** the `/metrics` server listens on `:9090`

#### Scenario: Custom listen address

- **WHEN** `export.prometheus.listen_address: ":9464"` is configured with the bridge enabled
- **THEN** the `/metrics` server listens on `:9464`

#### Scenario: Prometheus bridge reads same metric state as OTLP

- **WHEN** both OTLP and Prometheus outputs are enabled
- **THEN** both expose identical metric values (no separate collection; one MeterProvider, two exporters)

#### Scenario: Exposed names equal registry names

- **WHEN** every registry metric is recorded and `/metrics` is scraped
- **THEN** the set of metric family names equals the set of registry metric names, and every sample name is its family name, or for histograms the family name plus `_bucket`, `_sum` or `_count`

#### Scenario: OpenMetrics scrape keeps counter types

- **WHEN** `/metrics` is scraped with `Accept: application/openmetrics-text;version=1.0.0`
- **THEN** the response uses the Prometheus text format, the family names equal the registry names, and registry counters are typed `counter`

#### Scenario: No suffixes added

- **WHEN** `github.pr.automerged` (counter) and `github.pr.age` (unit `s`) are recorded and `/metrics` is scraped
- **THEN** the exposition contains `github.pr.automerged` and `github.pr.age`, and no `github.pr.automerged_total` or `github.pr.age_seconds`

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
- **THEN** `github_exporter.api.errors` counter is incremented with `exporter.target` and `github.api.endpoint` attributes

#### Scenario: OTLP error counter incremented

- **WHEN** an OTLP push fails
- **THEN** `github_exporter.otlp.errors` counter is incremented

### Requirement: Configurable OTLP headers and TLS

The system SHALL support configuring arbitrary HTTP headers (e.g., `Authorization`) and TLS settings for the OTLP endpoint.

#### Scenario: Auth header injected

- **WHEN** `export.otlp.headers: {Authorization: "Bearer <token>"}` is configured
- **THEN** all OTLP push requests include that header

#### Scenario: Multiple arbitrary headers injected

- **WHEN** `export.otlp.headers` contains more than one entry, e.g. `Authorization` and `X-Scope-OrgID`
- **THEN** all OTLP push requests include every configured header

#### Scenario: TLS verification on by default

- **WHEN** `export.otlp.tls.insecure` is not set or is `false`
- **THEN** OTLP connections verify the server's TLS certificate

#### Scenario: Insecure TLS opt-in

- **WHEN** `export.otlp.tls.insecure: true` is configured
- **THEN** OTLP/HTTP skips TLS certificate verification and OTLP/gRPC connects without TLS
