## ADDED Requirements

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
