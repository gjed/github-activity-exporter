## MODIFIED Requirements

### Requirement: OTLP push as primary export

The system SHALL push metrics to an OTLP-compatible endpoint every `export.interval` and after each collection cycle. Every push SHALL carry the latest collected values of every target. OTLP is the required primary output — the exporter MUST NOT start without a configured OTLP endpoint.

#### Scenario: Metrics pushed after each collection cycle

- **WHEN** a collection cycle completes
- **THEN** all collected metrics are pushed to the configured OTLP endpoint within the same cycle

#### Scenario: One push per target cycle

- **WHEN** N targets are configured and each completes a collection cycle
- **THEN** the exporter pushes once per completed target cycle (N pushes per interval), each push carrying the latest data of all targets

#### Scenario: Periodic push between cycles

- **WHEN** `collection_interval` is `15m` and `export.interval` is `60s`
- **THEN** the exporter pushes the latest values of every series at least every 60 seconds between cycles

#### Scenario: Repeated pushes do not double-count

- **WHEN** a counter or histogram is recorded once and the exporter then pushes several times
- **THEN** every push carries the same cumulative value and start time

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

### Requirement: Configurable collection interval

The system SHALL start a collection cycle per target every `collection_interval`, measured from the start of the previous cycle, and push its metrics via OTLP at the end of the cycle. When a cycle runs longer than `collection_interval`, the next cycle SHALL start as soon as it finishes: cycles of one target never overlap and missed periods are not caught up.

#### Scenario: Default collection interval

- **WHEN** `collection_interval` is not configured
- **THEN** metrics are collected every 5 minutes

#### Scenario: Custom interval respected

- **WHEN** `collection_interval: 15m` is set
- **THEN** collection and its end-of-cycle OTLP push occur approximately every 15 minutes

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

#### Scenario: Scrape duration buckets cover long cycles

- **WHEN** a collection cycle takes 1040 seconds
- **THEN** it falls in the `1800` bucket of `github_exporter.scrape.duration`, whose explicit boundaries are `1, 5, 10, 30, 60, 120, 300, 600, 900, 1800, 3600` seconds

#### Scenario: API error counter incremented

- **WHEN** a GitHub API call returns an error
- **THEN** `github_exporter.api.errors` counter is incremented with `exporter.target` and `github.api.endpoint` attributes

#### Scenario: API error series exported before the first error

- **WHEN** a target's collection loop starts
- **THEN** `github_exporter.api.errors` is exported with value 0 for that target and every `github.api.endpoint` value in the registry, from the first push

#### Scenario: OTLP error counter incremented

- **WHEN** an OTLP push fails
- **THEN** `github_exporter.otlp.errors` counter is incremented

#### Scenario: Overrun counter incremented

- **WHEN** a collection cycle runs longer than `collection_interval`
- **THEN** `github_exporter.scrape.overruns` counter is incremented with the `exporter.target` attribute

## ADDED Requirements

### Requirement: Configurable export interval

The system SHALL push metrics via OTLP every `export.interval`, independent of `collection_interval`. `export.interval` SHALL default to `60s` and MUST be positive.

#### Scenario: Default export interval

- **WHEN** `export.interval` is not configured
- **THEN** the exporter pushes every 60 seconds in addition to the end-of-cycle pushes

#### Scenario: Non-positive export interval is fatal

- **WHEN** `export.interval` is `0s` or negative
- **THEN** the exporter logs an error naming `export.interval` and exits with a non-zero status before collecting anything
