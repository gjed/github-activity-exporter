# Proposal

## Why

The exporter reports nothing about its own memory, GC or goroutines, so a leak only shows up as an OOM kill, and unbounded PR pagination makes memory a real risk on large orgs. For GitHub it records only a total API error count, with no latency and no per-endpoint or per-status breakdown ([#74](https://github.com/gjed/github-activity-exporter/issues/74)). The self-telemetry foundation from [#73](https://github.com/gjed/github-activity-exporter/issues/73) is on main, so the metrics can now plug into its MeterProvider.

## What Changes

- Go runtime metrics (`go.memory.used`, `go.memory.limit`, `go.memory.allocated`, `go.memory.allocations`, `go.memory.gc.goal`, `go.goroutine.count`, `go.processor.limit`, `go.config.gogc`, `go.schedule.duration`) from `go.opentelemetry.io/contrib/instrumentation/runtime`, on the self-telemetry MeterProvider.
- HTTP client metrics (`http.client.request.duration`, `http.client.request.body.size`) from `otelhttp` for every GitHub call: REST, GraphQL, the startup credential ping and the GitHub App installation token exchange.
- Bounded `url.template` for the routes the exporter calls (`/graphql`, `/repos/{owner}/{repo}/issues`, ...), omitted for any other path; `error.type` set to the status code for 4xx/5xx responses. No full URL and no repo name in any attribute.
- `otelhttp` sits inside the existing transport chain: the per-pool rate-limit pause and the 401 auth hook behind `/healthz` ([#18](https://github.com/gjed/github-activity-exporter/issues/18)) see the same requests and responses as before.
- Both are active only when `telemetry.enabled` and `telemetry.metrics.enabled` are true and no `OTEL_*` variable turns metrics off. Inactive, no instrumentation is installed and no `go.*` or `http.*` metric is exported on OTLP or `/metrics`.
- Weaver registry: import the emitted metrics from the pinned OTel semantic conventions dependency instead of redefining them, and refine each with `annotations.prometheus` ([#57](https://github.com/gjed/github-activity-exporter/issues/57)). The registry policy, the dashboard generator and the `/metrics` live-check read the annotation from the refinement.
- Dashboard: an "Exporter internals" row with runtime memory, GC and goroutines, and GitHub API latency p50/p90 per `url.template` and status, generated from the annotations. The e2e config turns the signal on so every new query runs against Prometheus 3.x.
- Helm chart: a values example that enables self-telemetry metrics. No template change.
- README: the new metrics, their activation and the attributes they carry.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `self-telemetry`: adds the Go runtime metrics and GitHub HTTP client metrics requirements, their activation, cardinality bounds and transport-order guarantees.
- `weaver-schema`: the registry may declare emitted metrics by importing them from the OTel semantic conventions dependency; the naming contract, the dashboard generator and the `/metrics` live-check accept the annotation on a refinement of an imported metric; the dashboard gains the "Exporter internals" row.

## Impact

- Code: `internal/telemetry` (runtime instrumentation and the `go.schedule.duration` producer on both readers), `internal/github` (instrumented base transport, route templates, authenticators take an HTTP client), `cmd/github-activity-exporter/main.go` (wiring).
- Dependencies: `go.opentelemetry.io/contrib/instrumentation/runtime` and `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` (v0.72.0, the contrib line matching otel v1.47.0).
- Registry and generation: new `registry/telemetry.yaml` (imports and refinements), `policies/registry/prometheus.rego`, `policies/live_check/prometheus.rego`, `templates/grafana/` (generated from the v2 resolved registry), `templates/go/weaver.yaml` (no constants for imported metrics), generated `dashboards/github-activity.json`.
- Tests: `internal/github` transport-order tests, `internal/telemetry` tests, `tests/integration` OTLP and `/metrics` live-checks with the signal on and off, `tests/e2e` config.
- Chart: `charts/github-activity-exporter/examples/self-telemetry-values.yaml`.
- Overlap: H2 (telemetry-profiles) touches the telemetry config section and README; rebase whichever merges second.
