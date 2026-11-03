# Spec Delta

## MODIFIED Requirements

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
