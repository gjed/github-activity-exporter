## ADDED Requirements

### Requirement: OTLP logs

When the logs signal is active, every record the exporter logs at or above the stdout level SHALL also be exported as an OTLP log record with the message as body, its attributes and the shared resource. The JSON log on stdout SHALL keep every record it has today.

#### Scenario: Logs signal active

- **WHEN** `telemetry.enabled` and `telemetry.logs.enabled` are true and a collection cycle runs
- **THEN** the cycle's log lines are on stdout and the same records arrive at the OTLP logs endpoint with the same message and attributes

#### Scenario: Logs signal inactive

- **WHEN** `telemetry.logs.enabled` is false
- **THEN** no LoggerProvider or OTLP log exporter exists, no connection is opened for logs, and the stdout JSON is byte-for-byte what the exporter writes without the bridge

#### Scenario: Below the stdout level

- **WHEN** a record is logged at debug and the stdout level is info
- **THEN** it is neither on stdout nor exported over OTLP

### Requirement: Log severity

An OTLP log record SHALL carry the SeverityNumber of the OTel log data model for its `slog` level: Debug 5, Info 9, Warn 13, Error 17, with the level name as SeverityText.

#### Scenario: Warning

- **WHEN** the exporter logs a warning with the logs signal active
- **THEN** the OTLP record has SeverityNumber 13 and SeverityText `WARN`

### Requirement: Log trace correlation

A record logged inside a span SHALL carry that span's trace and span IDs: as the trace context of the OTLP record and, while the logs signal is active, as `trace_id` and `span_id` keys on stdout. A record logged outside any span SHALL carry neither.

#### Scenario: Record from a repo worker

- **WHEN** traces and logs are active and a PR collector repo unit running in a `Limiter` worker logs a rate-limit pause
- **THEN** the OTLP record and the stdout line carry the trace ID of the cycle trace and the span ID of a span of that repo unit

#### Scenario: Failed repo

- **WHEN** traces and logs are active and a repo fails in the PR collector
- **THEN** its failure record carries the trace ID of the cycle and the span ID of the repo's `github_exporter.repo` span

#### Scenario: Traces inactive

- **WHEN** logs are active and traces are not
- **THEN** OTLP records have no trace context and stdout lines have no `trace_id` or `span_id` key

### Requirement: Log attribute keys

A log attribute naming something the registry has an attribute for SHALL use the registry name: `exporter.target`, `github.org`, `github.repo`, `github.api.rate_limit.resource`. The keys are the same on stdout and OTLP in every mode.

#### Scenario: Failed repo keys

- **WHEN** the issue collector fails for repo `acme/api` of target `prod`
- **THEN** the record has `exporter.target=prod`, `github.org=acme` and `github.repo=api`, and no `target` or `repo` key

### Requirement: No credentials in logs

No log record, on stdout or OTLP, SHALL contain a GitHub PAT, a GitHub App private key, a GitHub App installation token, or a header value from `export.otlp.headers` or from the variable named by `export.otlp.headers_env`.

#### Scenario: Planted secrets

- **WHEN** the exporter runs a cycle with logs active, a PAT target, a GitHub App target and OTLP headers from both sources, each holding a unique planted value
- **THEN** none of the planted values occurs on stdout or in any received OTLP log payload

### Requirement: OTLP export errors stay on stdout

Errors reported by the OTLP exporters (the OTel SDK error handler and the product metrics push) SHALL be logged on stdout only and never exported as OTLP log records.

#### Scenario: Log backend rejects exports

- **WHEN** the OTLP endpoint rejects log and metric exports, then accepts them
- **THEN** the export errors are on stdout and none of the OTLP log records received afterwards is an export error
