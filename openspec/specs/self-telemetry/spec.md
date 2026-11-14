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

### Requirement: GitHub HTTP client metrics

When self-telemetry metrics are active, every HTTP request the exporter sends to GitHub, including the credential check and the App token exchange, SHALL be recorded as `http.client.request.duration` and `http.client.request.body.size`. The duration SHALL cover one HTTP round trip, without rate-limit or token waits. When self-telemetry metrics are inactive, no `http.*` metric SHALL be exported and the GitHub HTTP transport SHALL be the uninstrumented one.

#### Scenario: REST and GraphQL calls recorded

- **WHEN** self-telemetry metrics are active and a collection cycle lists the org's repositories over REST and queries pull requests and issues over GraphQL
- **THEN** `http.client.request.duration` has data points with `http.request.method`, `http.response.status_code`, `server.address`, `server.port` and `url.template` `/orgs/{org}/repos` for the REST call and `/graphql` for the GraphQL calls

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

HTTP client metric attributes SHALL have bounded cardinality. `url.template` SHALL be set only for the routes the exporter calls, as an absolute path template under the target's API root (for example `/api/v3/orgs/{org}/repos` on GitHub Enterprise Server), and omitted for any other path. No attribute SHALL carry a full URL, a query string, an org, user, repo or installation identifier, or a token.

#### Scenario: Repo names stay out of attributes

- **WHEN** the exporter lists the repositories of `acme` and queries `acme/widgets` with self-telemetry metrics active
- **THEN** no `http.client.request.duration` attribute value contains `acme` or `widgets`, and `url.template` is `/orgs/{org}/repos` or `/graphql`

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

### Requirement: Inherited self-telemetry metrics in the product push

When self-telemetry metrics are active and neither `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` nor `OTEL_EXPORTER_OTLP_ENDPOINT` is set, they SHALL travel in the product OTLP push, every `export.interval` and after each cycle, with one resource and one `target_info`. `OTEL_METRIC_EXPORT_INTERVAL` SHALL NOT apply then. With an env endpoint they SHALL keep their own OTLP push.

#### Scenario: One push, one target_info

- **WHEN** self-telemetry metrics are active, no OTLP endpoint env var is set, and the exporter pushes to a stock Prometheus 3.x without an out-of-order window
- **THEN** `go.*` and `http.*` metrics arrive in the same requests as the GitHub metrics, `target_info` samples have strictly increasing timestamps, and Prometheus rejects no sample as out of order

#### Scenario: Runtime data points carry the push time

- **WHEN** self-telemetry metrics are active and two pushes are less than 15 seconds apart
- **THEN** `go.schedule.duration` in the second push has a later timestamp than in the first

#### Scenario: Export interval variable ignored when inherited

- **WHEN** self-telemetry metrics inherit `export.otlp` and `OTEL_METRIC_EXPORT_INTERVAL=5000` is set
- **THEN** self-telemetry metrics are pushed on the `export.interval` cadence, not every 5 seconds

#### Scenario: Env endpoint keeps a separate push

- **WHEN** self-telemetry metrics are active and `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` is set
- **THEN** self-telemetry metrics are pushed to that endpoint every `OTEL_METRIC_EXPORT_INTERVAL` (default 60 s), and the product push carries no `go.*` or `http.*` metric

#### Scenario: Self-telemetry failure does not block the product push

- **WHEN** collecting the inherited self-telemetry metrics fails
- **THEN** the product push still carries the GitHub metrics and the error is logged

### Requirement: Collection cycle traces

With traces active, the system SHALL record one root span `github_exporter.cycle` per target cycle, with `exporter.target`, `github_exporter.cycle.outcome` (`success`, `partial`, `failure`) and, after a successful discovery, `github_exporter.cycle.repos`. Its children SHALL be one `github_exporter.collect` span per collector (`github_exporter.collector`, `github_exporter.failed_repos`) and one `github_exporter.flush` span.

#### Scenario: Cycle span tree

- **WHEN** a cycle of a target with the PR, issue and dashboard collectors completes
- **THEN** its trace has one `github_exporter.cycle` root span with three `github_exporter.collect` children and one `github_exporter.flush` child

#### Scenario: Discovery fails

- **WHEN** repo discovery fails in a cycle
- **THEN** the cycle span has `github_exporter.cycle.outcome` `failure`, status `Error` and `error.type`, and no collector spans

#### Scenario: Some repos fail

- **WHEN** discovery succeeds and at least one repo fails in a collector
- **THEN** the cycle span has `github_exporter.cycle.outcome` `partial` and that collector span has status `Error` and the number of failed repos

### Requirement: Worker pool unit spans

With traces active, the system SHALL record one span per started unit of the target's worker pool, as a child of its collector span whatever goroutine runs it: `github_exporter.repo` (`github.org`, `github.repo`) per PR or issue title-filter repo, `github_exporter.batch` (`github_exporter.batch.size`, `github_exporter.failed_repos`) per GraphQL batch. A failed unit SHALL have status `Error` and `error.type`; a unit that never starts, no span.

#### Scenario: Concurrent units stay in the cycle trace

- **WHEN** a cycle runs with `collection.concurrency` 4
- **THEN** every repo and batch span has the cycle's trace ID and its collector span as parent, and no unit span is a root

#### Scenario: Failed repo in a batch

- **WHEN** GitHub answers a repo of a batched issue count query with a GraphQL `NOT_FOUND` error
- **THEN** that batch's `github_exporter.batch` span has status `Error`, `github_exporter.failed_repos` 1 and `error.type` `NOT_FOUND`

### Requirement: GitHub HTTP client spans

With traces active, every GitHub round trip SHALL be one semconv `http.client` span named `{method} {url.template}`, or `{method}` outside the known routes, with `url.template`, `graphql.operation.type` and `graphql.operation.name` on GraphQL calls (the name when the document has one), and `http.request.resend_count` on retries. Span names SHALL NOT contain a full URL, org, repo or installation. The transport SHALL NOT send trace context to GitHub.

#### Scenario: GraphQL batch request

- **WHEN** the issue collector sends a batched issue count query
- **THEN** its client span is named `POST /graphql` and carries `url.template` `/graphql`, `graphql.operation.type` `query` and `graphql.operation.name` `IssueCountBatch`

#### Scenario: REST discovery request

- **WHEN** discovery requests `/orgs/zextras/repos?page=2`
- **THEN** the client span is named `GET /orgs/{org}/repos`

#### Scenario: Retried request

- **WHEN** a request is sent again after a rate limit response
- **THEN** the second attempt is its own client span with `http.request.resend_count` 1

### Requirement: Rate-limit wait spans

With traces active, each wait of a request for a paused pool or a secondary rate limit pause SHALL be a `github_exporter.rate_limit.wait` span in the request's context, lasting as long as the wait, with `github.api.rate_limit.resource` (the request's pool) and `github_exporter.rate_limit.reason` `primary` or `secondary`. A request that does not wait SHALL record no wait span.

#### Scenario: Primary pool paused

- **WHEN** a request to the `graphql` pool waits for the pool's window reset
- **THEN** its unit span has a `github_exporter.rate_limit.wait` child with `github.api.rate_limit.resource` `graphql` and `github_exporter.rate_limit.reason` `primary`

#### Scenario: Secondary pause

- **WHEN** a secondary rate limit pauses the target while other units have requests pending
- **THEN** each held request records a wait span with `github_exporter.rate_limit.reason` `secondary`

### Requirement: Error recording on spans

The system SHALL mark a failed span with status `Error`, a description and a bounded `error.type`: the GraphQL error type when GitHub returned one, the HTTP status code for a REST error response, `_OTHER` otherwise. It SHALL NOT record exceptions as span events, as semconv 1.41.1 deprecates exceptions on spans.

#### Scenario: No exception events

- **WHEN** a repo fails
- **THEN** its span has no span events

### Requirement: No tracing of the exporter's own export

The system SHALL NOT create spans for its own OTLP exports of metrics, traces or logs.

#### Scenario: Flush has no children

- **WHEN** the per-cycle flush pushes metrics over OTLP with traces active
- **THEN** the `github_exporter.flush` span has no child spans and no other span is recorded for the export

### Requirement: Trace sampling

The system SHALL sample traces with the SDK sampler from `OTEL_TRACES_SAMPLER` and `OTEL_TRACES_SAMPLER_ARG`, default `parentbased_always_on`, so a cycle trace is kept or dropped whole.

#### Scenario: Ratio sampler

- **WHEN** `OTEL_TRACES_SAMPLER=parentbased_traceidratio` and `OTEL_TRACES_SAMPLER_ARG=0.1`
- **THEN** about one cycle in ten is exported, each with all its spans

### Requirement: Traces off cost nothing

With traces inactive, the system SHALL use the no-op tracer everywhere, SHALL NOT read GraphQL request bodies for span attributes, and SHALL leave the GitHub transport and the exported metrics as they are with traces active.

#### Scenario: Metric output independent of traces

- **WHEN** the same cycle runs with traces active and inactive
- **THEN** OTLP and `/metrics` carry the same metric names, attributes and values
