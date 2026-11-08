# metric-export Specification

## Purpose

Export metrics through an OTel MeterProvider with OTLP (HTTP or gRPC) as the primary output, an optional Prometheus /metrics bridge, and exporter self-metrics.

## Requirements

### Requirement: OTLP push as primary export

The system SHALL push metrics to an OTLP-compatible endpoint after each collection cycle. OTLP is the required primary output — the exporter MUST NOT start without a configured OTLP endpoint.

#### Scenario: Metrics pushed after each collection cycle

- **WHEN** a collection cycle completes
- **THEN** all collected metrics are pushed to the configured OTLP endpoint within the same cycle

#### Scenario: One push per target cycle

- **WHEN** N targets are configured and each completes a collection cycle
- **THEN** the exporter pushes once per completed target cycle (N pushes per interval), each push carrying the latest data of all targets

#### Scenario: Failed push does not stop collection

- **WHEN** the push at the end of a collection cycle fails
- **THEN** the exporter logs a warning, increments `github_exporter.otlp.errors`, and the next collection cycle still runs

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

The system SHALL start a collection cycle per target every `collection_interval`, measured from the start of the previous cycle, and push its metrics via OTLP at the end of the cycle. When a cycle runs longer than `collection_interval`, the next cycle SHALL start as soon as it finishes: cycles of one target never overlap and missed periods are not caught up.

#### Scenario: Default collection interval

- **WHEN** `collection_interval` is not configured
- **THEN** metrics are collected and pushed every 5 minutes

#### Scenario: Custom interval respected

- **WHEN** `collection_interval: 15m` is set
- **THEN** collection and OTLP push occur approximately every 15 minutes

#### Scenario: Period measured from cycle start

- **WHEN** `collection_interval` is `15m` and a cycle starting at 12:00 takes 10 minutes
- **THEN** the next cycle starts at 12:15

#### Scenario: Overrunning cycle

- **WHEN** `collection_interval` is `15m` and a cycle starting at 12:00 takes 17 minutes
- **THEN** the next cycle starts at 12:17, a warning is logged naming the target, and `github_exporter.scrape.overruns` is incremented for the target

#### Scenario: No catch-up after an overrun

- **WHEN** `collection_interval` is `15m`, a cycle starting at 12:00 takes 40 minutes and the next one takes 1 minute
- **THEN** the next cycle starts at 12:40 and the one after it at 12:55

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

#### Scenario: Overrun counter incremented

- **WHEN** a collection cycle runs longer than `collection_interval`
- **THEN** `github_exporter.scrape.overruns` counter is incremented with the `exporter.target` attribute

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

### Requirement: Shared resource and instrumentation scope

Product metrics SHALL carry the resource shared with self-telemetry: `service.name` (default `github-activity-exporter`), `service.version` (the build version) and `service.instance.id` (a UUID generated at startup), with `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` overriding them per the OTel spec. Every meter SHALL use the scope `github.com/gjed/github-activity-exporter` versioned with the build version.

#### Scenario: Default resource

- **WHEN** product metrics are exported with no `OTEL_*` resource variable set
- **THEN** the resource carries `service.name=github-activity-exporter`, `service.version` equal to the version `-version` prints, and a UUID `service.instance.id`

#### Scenario: Env overrides resource

- **WHEN** `OTEL_SERVICE_NAME=gh-exp` and `OTEL_RESOURCE_ATTRIBUTES=service.instance.id=pod-1` are set
- **THEN** the resource carries `service.name=gh-exp` and `service.instance.id=pod-1`

#### Scenario: One scope for all targets

- **WHEN** two targets export metrics
- **THEN** every metric has scope name `github.com/gjed/github-activity-exporter` and scope version equal to the build version

### Requirement: OTLP headers from an environment variable

When `export.otlp.headers_env` names an env var, the system SHALL read OTLP headers from it as comma-separated `name=value` pairs in the `OTEL_EXPORTER_OTLP_HEADERS` format and merge them into `export.otlp.headers`, the env var winning for the same header name. Header names SHALL be compared case-insensitively. The merged headers SHALL apply wherever `export.otlp.headers` apply.

#### Scenario: Auth header from the environment

- **WHEN** `export.otlp.headers_env: OTLP_HEADERS` is configured and `OTLP_HEADERS=authorization=Bearer s3cret`
- **THEN** every OTLP push request carries `authorization: Bearer s3cret`

#### Scenario: Merged with file headers

- **WHEN** `export.otlp.headers` holds `X-Scope-OrgID: tenant-a` and `Authorization: Bearer file`, and the `headers_env` variable holds `authorization=Bearer env`
- **THEN** OTLP push requests carry `x-scope-orgid: tenant-a` and `authorization: Bearer env`

#### Scenario: Percent-encoded value

- **WHEN** the `headers_env` variable holds `x-token=a%2Cb`
- **THEN** OTLP push requests carry `x-token: a,b`

#### Scenario: Inherited by self-telemetry

- **WHEN** a self-telemetry signal uses the `export.otlp` destination and `headers_env` supplies `authorization`
- **THEN** its exports carry that header

### Requirement: Header environment errors

The exporter SHALL NOT start when `export.otlp.headers_env` is set and the variable is unset, empty, or holds an entry that is not a valid `name=value` pair. The error SHALL name the variable and the entry position and SHALL NOT contain any header value.

#### Scenario: Variable not set

- **WHEN** `export.otlp.headers_env: OTLP_HEADERS` is configured and `OTLP_HEADERS` is unset or empty
- **THEN** the exporter exits at startup with an error naming `OTLP_HEADERS`

#### Scenario: Malformed entry

- **WHEN** the `headers_env` variable holds `authorization=Bearer s3cret,garbage`
- **THEN** the exporter exits at startup with an error naming the variable and entry 2, and the error does not contain `s3cret`

### Requirement: Header values never logged

The system SHALL NOT write any OTLP header value, from `export.otlp.headers` or `headers_env`, to a log line or error message.

#### Scenario: Push rejected

- **WHEN** an OTLP push carrying `authorization: Bearer s3cret` is rejected
- **THEN** no log line contains `s3cret`

### Requirement: Auth rejection warning

The first time in the process an OTLP push is rejected as unauthenticated or forbidden (HTTP 401/403, gRPC `Unauthenticated`/`PermissionDenied`), the system SHALL log one warning that names `export.otlp.headers` and `export.otlp.headers_env`. `/healthz` and `/readyz` SHALL NOT change because of OTLP push failures.

#### Scenario: First push gets 401

- **WHEN** the first OTLP push gets HTTP 401
- **THEN** one auth-rejection warning is logged, `github_exporter.otlp.errors` is incremented, and `/healthz` stays 200

#### Scenario: 401 after another failure

- **WHEN** the first OTLP push fails because the endpoint is not ready and the next one gets HTTP 401
- **THEN** one auth-rejection warning is logged for the 401

#### Scenario: Later rejections

- **WHEN** later pushes also get 401
- **THEN** no further auth-rejection warning is logged and each failure still increments `github_exporter.otlp.errors`
