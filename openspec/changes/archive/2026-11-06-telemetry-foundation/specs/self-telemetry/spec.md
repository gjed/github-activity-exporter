# Spec Delta

## Purpose

Opt-in telemetry about the exporter itself (metrics, traces, logs, profiles), configured per signal and through the standard OpenTelemetry SDK environment variables, isolated from the GitHub activity metrics the exporter exists to export.

## ADDED Requirements

### Requirement: Telemetry configuration section

The config file SHALL accept a `telemetry` section with `enabled` (default `false`) and `metrics.enabled`, `traces.enabled`, `logs.enabled`, `profiles.enabled` (each default `true`). Each key SHALL be overridable by a `GITHUB_ACTIVITY_EXPORTER_` env var, upper-cased with `.` replaced by `_`.

#### Scenario: Defaults

- **WHEN** the config has no `telemetry` section
- **THEN** `telemetry.enabled` is `false` and every per-signal `enabled` is `true`

#### Scenario: Env override

- **WHEN** `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_ENABLED=true` and `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_LOGS_ENABLED=false` are set and the file sets neither
- **THEN** `telemetry.enabled` is `true` and `telemetry.logs.enabled` is `false`

#### Scenario: Invalid value is fatal

- **WHEN** a `telemetry` toggle is set, in the file or by env var, to a value that is not a boolean
- **THEN** the exporter logs an error naming the key and exits with a non-zero status before collecting anything

### Requirement: Signal activation

A self-telemetry signal SHALL be active only when `telemetry.enabled` is true, its own `enabled` is true, `OTEL_SDK_DISABLED` is not `true`, and, for traces, metrics and logs, `OTEL_<SIGNAL>_EXPORTER` is not `none`. Environment variables SHALL only deactivate a signal, never activate one.

#### Scenario: Global off overrides signal on

- **WHEN** `telemetry.enabled` is `false` and `telemetry.traces.enabled` is `true`
- **THEN** traces are inactive

#### Scenario: Signal off overrides global on

- **WHEN** `telemetry.enabled` is `true` and `telemetry.metrics.enabled` is `false`
- **THEN** self-telemetry metrics are inactive and the other signals are active

#### Scenario: Exporter none turns a signal off

- **WHEN** `telemetry.enabled` is `true` and `OTEL_LOGS_EXPORTER=none` is set
- **THEN** logs are inactive and traces and metrics are active

#### Scenario: Env cannot turn a signal on

- **WHEN** `telemetry.traces.enabled` is `false` and `OTEL_TRACES_EXPORTER=otlp` is set
- **THEN** traces are inactive

#### Scenario: Unsupported exporter value

- **WHEN** `OTEL_METRICS_EXPORTER` is set to a value other than `otlp` or `none`
- **THEN** the exporter logs a warning naming the variable and value and treats it as `otlp`

#### Scenario: SDK disabled turns off self-telemetry only

- **WHEN** `telemetry.enabled` is `true` and `OTEL_SDK_DISABLED=true` is set
- **THEN** every self-telemetry signal is inactive, an info log says so, and product metrics are exported as without the variable

### Requirement: Inactive signals cost nothing

For an inactive signal the exporter SHALL NOT create an SDK provider or exporter for it, SHALL NOT open any network connection for it, and SHALL hand its consumers a no-op provider.

#### Scenario: Everything off by default

- **WHEN** the exporter runs with the default config
- **THEN** no self-telemetry provider is created, the global TracerProvider and LoggerProvider are the no-op defaults, and the only OTLP traffic is the product metrics push

### Requirement: OTLP destination for self-telemetry

An active traces, metrics or logs signal SHALL export over OTLP. When neither `OTEL_EXPORTER_OTLP_<SIGNAL>_ENDPOINT` nor `OTEL_EXPORTER_OTLP_ENDPOINT` is set, it SHALL use the `export.otlp` endpoint, protocol, headers and TLS. Otherwise it SHALL be configured from the `OTEL_EXPORTER_OTLP_*` variables only, signal-specific over generic, without any `export.otlp` setting.

#### Scenario: Inherits export.otlp

- **WHEN** traces are active, no OTLP endpoint env var is set, and `export.otlp` is `protocol: http`, `endpoint: https://gw.example.com/otlp` with an `authorization` header
- **THEN** spans are sent over OTLP/HTTP to `https://gw.example.com/otlp/v1/traces` with that header

#### Scenario: Signal-specific endpoint wins

- **WHEN** `OTEL_EXPORTER_OTLP_ENDPOINT=http://a:4318` and `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://b:4318/v1/traces` are set
- **THEN** spans go to `http://b:4318/v1/traces` and logs go to `http://a:4318/v1/logs`

#### Scenario: Env endpoint does not reuse file credentials

- **WHEN** `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT` is set and `export.otlp.headers` holds an `authorization` header
- **THEN** log exports do not carry the `export.otlp` headers

#### Scenario: Header env overrides inherited headers

- **WHEN** no OTLP endpoint env var is set and `OTEL_EXPORTER_OTLP_HEADERS` is set
- **THEN** self-telemetry exports carry the env headers instead of `export.otlp.headers`

#### Scenario: Sampler from env

- **WHEN** traces are active and `OTEL_TRACES_SAMPLER=traceidratio`, `OTEL_TRACES_SAMPLER_ARG=0.1` are set
- **THEN** the TracerProvider samples with a trace-ID ratio of 0.1; without the variables it uses `parentbased_always_on`

### Requirement: Product metrics isolation

No `telemetry` setting and no `OTEL_*` toggle SHALL change which product metrics (GitHub metrics and `github_exporter.*` self-metrics) are exported, where they are pushed, or when.

#### Scenario: Every toggle combination

- **WHEN** the exporter runs with any combination of `telemetry.enabled`, the per-signal toggles, `OTEL_SDK_DISABLED` and `OTEL_{TRACES,METRICS,LOGS}_EXPORTER`
- **THEN** every product metric is pushed to `export.otlp` and exposed on `/metrics` (when enabled) exactly as with the default config

### Requirement: Self-telemetry metrics on the Prometheus bridge

When self-telemetry metrics are active and the Prometheus bridge is enabled, metrics recorded on the self-telemetry MeterProvider SHALL also be exposed on `/metrics`, with one `target_info` family for the whole endpoint.

#### Scenario: Bridge exposes self-telemetry metrics

- **WHEN** self-telemetry metrics are active, the bridge is enabled, and a metric is recorded on the self-telemetry MeterProvider
- **THEN** `/metrics` exposes it alongside the product metrics and contains exactly one `target_info` family

### Requirement: Graceful shutdown

On SIGTERM or SIGINT, after the collection loops stop, the exporter SHALL flush and shut down every active self-telemetry provider within the existing shutdown deadline, after the product MeterProvider.

#### Scenario: Pending spans flushed on SIGTERM

- **WHEN** traces are active and the exporter receives SIGTERM with spans buffered
- **THEN** the buffered spans are exported before the process exits
