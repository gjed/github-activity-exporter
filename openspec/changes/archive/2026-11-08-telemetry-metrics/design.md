# Design

## Context

[#73](https://github.com/gjed/github-activity-exporter/issues/73) left a `telemetry.Telemetry` whose `MeterProvider()` is an SDK provider (periodic OTLP reader plus, with the bridge on, a Prometheus reader on the bridge registry) when metrics are active and a no-op provider otherwise. The GitHub client chain today is `http.Client → oauth2.Transport → rateLimitTransport → http.DefaultTransport`; `rateLimitTransport` classifies the pool, waits on a paused pool, reports 401s to the auth hooks and parses the rate-limit headers. The App token exchange and both credential pings use `http.DefaultClient` directly, outside that chain.

Probed on weaver 0.28 against the pinned semconv v1.41.1 dependency:

- `imports: metrics: [...]` in a `definition/2` file pulls the upstream definitions into `registry.metrics`, so the registry policy and live-check already see them.
- A `metric_refinements` entry with `ref: <imported metric>` can carry `annotations` (and attribute changes). The annotation lands on the refinement in the v2 resolved schema (`refinements.metrics`), not on the base metric in `registry.metrics`.
- The v1 template context (`.groups`, what `make generate` uses today) contains the imported base metric but not the refinement, so its annotations are invisible there. `registry generate --v2` exposes `refinements.metrics`, where every own metric appears as its own refinement with its annotations and an imported metric appears both bare and with the refinement's annotations.

Contrib v0.72.0 is the line that builds against otel v1.47.0. `runtime.Start` records eight `go.*` metrics through callbacks; `go.schedule.duration` comes only from `runtime.NewProducer()`, which has to be attached to each reader. `otelhttp.NewTransport` records `http.client.request.duration` and `http.client.request.body.size` with method, server address/port, scheme, protocol name/version, status code and, only for a transport error, `error.type`; extra attributes go through the `Labeler` it puts in the request context.

## Goals / Non-Goals

**Goals:**

- Runtime and GitHub HTTP client metrics on the existing self-telemetry MeterProvider, zero cost when inactive.
- One instrumented base transport for every GitHub call, including the token exchange and pings.
- Registry declares them by import + refinement; the existing contract tooling (policy, dashboard, both live-checks) covers them.

**Non-Goals:**

- Spans from `otelhttp` (that is [#75](https://github.com/gjed/github-activity-exporter/issues/75)): the transport gets a no-op TracerProvider and no propagator, so it neither creates spans nor injects trace headers into GitHub requests even when traces are active.
- A per-target attribute on HTTP client metrics, metric views, or changing the self-telemetry export interval.
- Bumping the semconv dependency pin (see Risks).

## Decisions

### D1. Runtime instrumentation lives in `internal/telemetry`

`newMeterProvider` attaches `runtime.NewProducer()` to the periodic reader (`sdkmetric.WithProducer`) and to the Prometheus reader (`promexporter.WithProducer`), and `New` calls `runtime.Start(runtime.WithMeterProvider(mp))` once the provider exists. Inactive metrics build no provider, so neither runs.

Why here and not in `main.go` through `MeterProvider()`: the producer must be wired into the readers, which only `internal/telemetry` builds. Alternative: expose a reader-option hook on `Settings` for `main.go` to fill. Rejected: an API with one caller that leaks reader construction out of the package.

### D2. One instrumented base transport, innermost

`internal/github` gets `NewTransport(mp metric.MeterProvider) http.RoundTripper`: with a nil provider it returns `http.DefaultTransport` unchanged; otherwise `otelhttp.NewTransport(routeTransport{http.DefaultTransport}, WithMeterProvider(mp), WithTracerProvider(noop), WithPropagators(empty))`. `main.go` builds it once from `tel.MeterProvider()` only when `tel.Active(SignalMetrics)` and passes it to `NewClient` (new `WithBaseTransport` option, replacing the hard-coded `http.DefaultTransport` under `rateLimitTransport`) and to the authenticators (an `HTTPClient` used for the token exchange and the ping, defaulting to `http.DefaultClient`).

Chain with metrics active: `http.Client → oauth2.Transport → rateLimitTransport → otelhttp.Transport → routeTransport → http.DefaultTransport`.

- `rateLimitTransport` still sees every request first and every response last, unchanged, so the pause, the gauges and the 401 hook behave as before; `otelhttp` only wraps the response body.
- The recorded duration is one network round trip: it excludes the pause wait and token fetches, which would otherwise show up as GitHub latency.
- Alternative: `otelhttp` outermost. Rejected: durations include local waits, and token-exchange requests would be nested inside the outer measurement.

### D3. `routeTransport` sets `url.template` and `error.type`

Inside `otelhttp`, so it reads the `Labeler` `otelhttp` put in the context: before the round trip it matches the path against a fixed route table and adds `url.template`; after it, for a 4xx/5xx response it adds `error.type` = status code (semconv rule for HTTP clients; `otelhttp` itself sets it only for transport errors). The table, relative to the target's API root path (empty on github.com, `/api/v3` on GHES), with the GraphQL path matched exactly:

| Route                                                | Caller                                    |
| ---------------------------------------------------- | ----------------------------------------- |
| `/graphql` (`/api/graphql` on GHES)                  | PR collector                              |
| `/repos/{owner}/{repo}/issues`                       | issue and Dependency Dashboard collectors |
| `/orgs/{org}/repos`                                  | discovery                                 |
| `/user`, `/app`                                      | credential ping                           |
| `/app/installations/{installation_id}/access_tokens` | App token exchange                        |

An unmatched path gets no `url.template`. `url.template` is the full absolute path template (prefix included), as semconv defines it. Bounded: one API root per target, fixed table. A new caller (for example H4's `/users/{username}/repos`) adds one row.

Alternative: the deprecated `otelhttp.WithMetricAttributesFn`. Rejected: deprecated, and it cannot see the response for `error.type`.

### D4. Registry: `registry/telemetry.yaml` with imports and refinements

A new file imports the eleven metrics from the `otel` dependency and declares one `metric_refinements` entry per metric (id `github_activity_exporter.<metric>`) with `annotations.prometheus` and `unref` for upstream attributes the instrumentation never attaches (for example `go.memory.detailed_type`), keeping "attributes block lists exactly what the code attaches". No metric or attribute is redefined.

Consequences on the tooling, because the annotation lives only on the refinement:

- `policies/registry/prometheus.rego`: for each metric in `registry.metrics`, require at least one refinement of the same name with `annotations.prometheus`, and check name/type on every refinement that declares it. Own metrics pass through their self-refinement, imported ones through the explicit refinement; an import without a refinement is a violation naming the metric.
- `templates/grafana/`: generate with `--v2`, filter `.refinements.metrics` to those with `annotations.prometheus` (unique by name). Macros switch from v1 keys (`metric_name`, attribute `name`) to v2 keys (`name`, attribute `key`). The rendered queries for the existing panels stay byte-identical; `make generate` diff proves it.
- `templates/go/weaver.yaml`: stays v1; the filter excludes groups whose provenance is the `otel` dependency, so no constants appear for imported metrics.
- `policies/live_check/prometheus.rego`: if live-check hands the base imported metric as `registry_group` (no annotation), the policy falls back to the type the registry policy already enforces for that instrument. Verified against a real `/metrics` scrape in the integration test; a wrong exposed type on an imported metric must still be a violation.

Alternative: redefine the metrics in `signals.yaml`. Rejected by the issue. Alternative: keep v1 generation and duplicate the annotations in the template. Rejected: the dashboard would no longer be derived from the annotations.

### D5. Dashboard "Exporter internals" row

Panels, all `per_target=false` (no `exporter.target` on these metrics; `$job` still applies): memory used by `go.memory.type` with heap goal and limit, heap allocation rate (`rate` of `go.memory.allocated`), goroutines, GitHub API latency p50 and p90 by `url.template` and `http.response.status_code`. The `quantile` shape gains the `per_target` flag the gauge shapes already have.

### D6. Tests

- `internal/github`: an `httptest` server and an SDK `ManualReader`. With the instrumented base transport: a 401 reaches the auth hook and is recorded with status 401; a low `X-RateLimit-Remaining` pauses the next request, the gauges update, and the recorded duration of the paused request excludes the wait; REST, GraphQL, token exchange and ping get their `url.template`; a repo name never appears in any attribute; unknown path has no `url.template`; 5xx carries `error.type`.
- `internal/telemetry`: with metrics active the readers produce the `go.*` metrics including `go.schedule.duration`; inactive produces none.
- `tests/integration`: OTLP and `/metrics` live-checks run twice, self-telemetry metrics on and off, with GitHub calls through a mock server and the instrumented client.
- `tests/e2e`: telemetry metrics on in the e2e config, with a short `OTEL_METRIC_EXPORT_INTERVAL`, so every new query returns data in Prometheus 3.x.

## Risks / Trade-offs

- [Contrib v0.72.0 records from semconv v1.43.0, the registry pins v1.41.1] → live-check with the signal on is the gate. If it reports a mismatch, the fix is bumping the pin in `registry/manifest.yaml` to the version contrib emits; that is a contract change, so it comes back for approval before landing in this PR.
- \[`go.schedule.duration` uses the runtime's own bucket layout, many more buckets than the SDK default\] → larger `/metrics` and OTLP payloads on the self-telemetry interval only (60 s default); accepted, the issue asks for it.
- [Weaver v2 refinements and imports are not yet stable in 0.28] → the contract tooling depends on them; the registry policy test and the generated-files check catch a behavior change on a weaver bump.
- \[The product and self-telemetry pipelines share one resource (#73) and push on separate schedules, so both write the same `target_info` series with interleaved timestamps; Prometheus without an out-of-order window rejects pushes, product ones included, with `out of order sample` (found by the e2e)\] → documented as a backend requirement in `README.md` (Prometheus `storage.tsdb.out_of_order_time_window`, Mimir/Grafana Cloud `out_of_order_time_window`); the e2e Prometheus runs with a 10m window.
- [A new GitHub route lands without a table row] → it is recorded without `url.template`, never with a raw path, so cardinality stays bounded.

## Migration Plan

Opt-in and additive: nothing changes unless `telemetry.enabled` is true. Rollback is `telemetry.metrics.enabled: false` or `OTEL_METRICS_EXPORTER=none`.
