# self-telemetry Specification

## Purpose

Opt-in telemetry about the exporter itself (metrics, traces, logs, profiles), configured per signal and through the standard OpenTelemetry SDK environment variables, isolated from the GitHub activity metrics the exporter exists to export.

## Requirements

### Requirement: Telemetry configuration section

The config file SHALL accept a `telemetry` section with `enabled` (default `false`), `metrics.enabled`, `traces.enabled`, `logs.enabled` (each default `true`) and `profiles.enabled` (default `false`). Each key SHALL be overridable by a `GITHUB_ACTIVITY_EXPORTER_` env var, upper-cased with `.` replaced by `_`.

#### Scenario: Defaults

- **WHEN** the config has no `telemetry` section
- **THEN** `telemetry.enabled` is `false`, `metrics.enabled`, `traces.enabled` and `logs.enabled` are `true`, and `profiles.enabled` is `false`

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

No `telemetry` setting and no `OTEL_*` variable other than `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` SHALL change which product metrics (GitHub metrics and `github_exporter.*` self-metrics) are exported, where and how they are pushed, or when.

#### Scenario: Every toggle combination

- **WHEN** the exporter runs with any combination of `telemetry.enabled`, the per-signal toggles, `OTEL_SDK_DISABLED` and `OTEL_{TRACES,METRICS,LOGS}_EXPORTER`
- **THEN** every product metric is pushed to `export.otlp` and exposed on `/metrics` (when enabled) exactly as with the default config

#### Scenario: OTLP exporter variables do not reach the product push

- **WHEN** `OTEL_EXPORTER_OTLP_{,METRICS_}*` headers, compression, timeout, certificate, temporality preference or histogram aggregation, or `OTEL_METRIC_EXPORT_TIMEOUT`, are set
- **THEN** the product push carries only the `export.otlp` headers, is uncompressed, uses cumulative temporality and explicit-bucket histograms, and keeps the default timeouts

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

### Requirement: Go runtime metrics

When self-telemetry metrics are active, the exporter SHALL record the Go runtime metrics of the OTel semantic conventions on the self-telemetry MeterProvider, pushed over OTLP on the self-telemetry export interval and exposed on `/metrics` when the Prometheus bridge is enabled. When self-telemetry metrics are inactive, no `go.*` metric SHALL be exported on OTLP or exposed on `/metrics`.

#### Scenario: Runtime metrics with the signal on

- **WHEN** `telemetry.enabled` and `telemetry.metrics.enabled` are true and the Prometheus bridge is enabled
- **THEN** the OTLP self-telemetry push and `/metrics` carry `go.memory.used`, `go.memory.allocated`, `go.memory.allocations`, `go.memory.gc.goal`, `go.goroutine.count`, `go.processor.limit`, `go.config.gogc` and `go.schedule.duration`

#### Scenario: Memory limit only when set

- **WHEN** self-telemetry metrics are active and `GOMEMLIMIT` is not set
- **THEN** `go.memory.limit` is not reported; with `GOMEMLIMIT=256MiB` it is reported as 268435456

#### Scenario: Runtime metrics with the signal off

- **WHEN** the exporter runs with the default config, or with `telemetry.metrics.enabled: false`, or with `OTEL_METRICS_EXPORTER=none`
- **THEN** neither the OTLP push nor `/metrics` carries any `go.*` metric

### Requirement: GitHub HTTP client metrics

When self-telemetry metrics are active, every HTTP request the exporter sends to GitHub, including the credential check and the App token exchange, SHALL be recorded as `http.client.request.duration` and `http.client.request.body.size`. The duration SHALL cover one HTTP round trip, without rate-limit or token waits. When self-telemetry metrics are inactive, no `http.*` metric SHALL be exported and the GitHub HTTP transport SHALL be the uninstrumented one.

#### Scenario: REST and GraphQL calls recorded

- **WHEN** self-telemetry metrics are active and a collection cycle lists issues over REST and queries pull requests over GraphQL
- **THEN** `http.client.request.duration` has data points with `http.request.method`, `http.response.status_code`, `server.address`, `server.port` and `url.template` `/repos/{owner}/{repo}/issues` for the REST call and `/graphql` for the GraphQL call

#### Scenario: App token exchange recorded

- **WHEN** self-telemetry metrics are active and a GitHub App target exchanges its JWT for an installation token
- **THEN** `http.client.request.duration` has a data point with `http.request.method` `POST` and `url.template` `/app/installations/{installation_id}/access_tokens`

#### Scenario: Error status

- **WHEN** GitHub answers a request with a 4xx or 5xx status
- **THEN** the data point carries `http.response.status_code` and `error.type` set to the status code; a request that fails without a response carries `error.type` and no `http.response.status_code`

#### Scenario: HTTP metrics with the signal off

- **WHEN** self-telemetry metrics are inactive
- **THEN** neither the OTLP push nor `/metrics` carries any `http.*` metric

### Requirement: Bounded HTTP client attributes

HTTP client metric attributes SHALL have bounded cardinality. `url.template` SHALL be set only for the routes the exporter calls, as an absolute path template under the target's API root (for example `/api/v3/repos/{owner}/{repo}/issues` on GitHub Enterprise Server), and omitted for any other path. No attribute SHALL carry a full URL, a query string, an org, user, repo or installation identifier, or a token.

#### Scenario: Repo names stay out of attributes

- **WHEN** the exporter lists issues of `acme/widgets` with self-telemetry metrics active
- **THEN** no `http.client.request.duration` attribute value contains `acme` or `widgets`, and `url.template` is `/repos/{owner}/{repo}/issues`

#### Scenario: Unknown route

- **WHEN** the exporter sends a request to a path that is not one of its known routes
- **THEN** the data point has no `url.template` attribute

### Requirement: HTTP instrumentation preserves GitHub client behavior

Recording HTTP client metrics SHALL NOT change how the GitHub client handles rate limits or authentication: with self-telemetry metrics active, the per-pool rate-limit pause, the rate-limit gauges and the 401 auth result reported to `/healthz` SHALL behave exactly as with them inactive.

#### Scenario: 401 still reaches the health tracker

- **WHEN** self-telemetry metrics are active and GitHub answers a request with 401
- **THEN** the auth result hook reports unauthorized, and the request is recorded with `http.response.status_code` 401

#### Scenario: Rate-limit pause still applies

- **WHEN** self-telemetry metrics are active and a response leaves a pool below the pause threshold with a future reset time
- **THEN** the next request to that pool waits for the reset, the rate-limit gauges are updated as without self-telemetry, and the recorded duration of that next request excludes the wait

### Requirement: Profiles push to an OpenTelemetry Collector

When the profiles signal is active, the exporter SHALL push gzip-compressed pprof profiles of its own process with `POST <telemetry.profiles.endpoint>/v1/pprof`, the push API of the OpenTelemetry Collector `pprof` receiver: every `telemetry.profiles.interval` (default `60s`) a CPU profile covering that interval, then heap and goroutine profiles.

#### Scenario: Profiles reach the Collector

- **WHEN** `telemetry.enabled` and `telemetry.profiles.enabled` are true, `telemetry.profiles.endpoint` points at a Collector `pprof` receiver, and the exporter runs for longer than one interval
- **THEN** the Collector receives CPU, heap and goroutine profiles

#### Scenario: Signal inactive

- **WHEN** `telemetry.enabled` is false, `telemetry.profiles.enabled` is false, or `OTEL_SDK_DISABLED=true`
- **THEN** no profile is collected and no connection is opened to `telemetry.profiles.endpoint`

#### Scenario: No pull endpoint

- **WHEN** the profiles signal is active
- **THEN** the health server answers `/debug/pprof/` with 404

#### Scenario: Failed push

- **WHEN** a push fails or the Collector answers with a non-2xx status
- **THEN** the exporter logs an error with the profile type and the cause, and pushes again at the next interval

### Requirement: Profiles configuration errors are not fatal

When `telemetry.enabled` and `telemetry.profiles.enabled` are true and `endpoint` is not an http(s) URL with a host, `interval` is not a positive duration, or `mutex_fraction` or `block_rate` is negative, the exporter SHALL log an error naming the key, keep the profiles signal inactive, and start normally.

#### Scenario: Missing endpoint

- **WHEN** `telemetry.enabled` and `telemetry.profiles.enabled` are true and `telemetry.profiles.endpoint` is empty
- **THEN** the exporter logs an error naming `telemetry.profiles.endpoint`, does not push profiles, and collects and exports GitHub metrics as usual

#### Scenario: Invalid rate

- **WHEN** the profiles signal is enabled and `mutex_fraction` is `-1`
- **THEN** the exporter logs an error naming `telemetry.profiles.mutex_fraction` and does not push profiles

### Requirement: Mutex and block profiling rates

`telemetry.profiles.mutex_fraction` and `telemetry.profiles.block_rate` SHALL default to `0` (off). When the profiles signal is active and a value is above `0`, the exporter SHALL set the Go runtime mutex profile fraction or block profile rate to it and push the mutex or block profile every interval; otherwise it SHALL leave the runtime rates at `0` and not push them.

#### Scenario: Off by default

- **WHEN** the profiles signal is active and neither key is set
- **THEN** no mutex or block profile is pushed and the runtime mutex fraction and block rate stay `0`

#### Scenario: Enabled

- **WHEN** the profiles signal is active, `mutex_fraction` is `100` and `block_rate` is `100000`
- **THEN** mutex and block profiles are pushed every interval with those runtime rates

#### Scenario: Ignored when inactive

- **WHEN** `mutex_fraction` is `100` and the profiles signal is inactive
- **THEN** the runtime mutex fraction stays `0`

### Requirement: Profiles shutdown

On SIGTERM or SIGINT, within the existing shutdown deadline, the exporter SHALL stop the profiler and push the CPU profile of the current, partial interval.

#### Scenario: Last window pushed

- **WHEN** the profiles signal is active and the exporter receives SIGTERM halfway through an interval
- **THEN** the CPU profile of that half interval is pushed and the process exits within the shutdown deadline
