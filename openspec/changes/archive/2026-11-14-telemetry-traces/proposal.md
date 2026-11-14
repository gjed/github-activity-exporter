# Proposal

## Why

`github_exporter.scrape.duration` says that a cycle was slow, not why ([#75](https://github.com/gjed/github-activity-exporter/issues/75)). Since [#91](https://github.com/gjed/github-activity-exporter/issues/91) a target collects up to `collection.concurrency` repos or batches at once and pauses on primary and secondary rate limits, so the time of a cycle is spread over parallel units, HTTP round trips and waits that no metric separates. A trace of the cycle shows which repo, which page and which rate-limit wait took the time. It is also the prerequisite for correlating logs to work ([#76](https://github.com/gjed/github-activity-exporter/issues/76)).

## What Changes

- With `telemetry.enabled` and `telemetry.traces.enabled` ([#73](https://github.com/gjed/github-activity-exporter/issues/73)), the exporter traces its own work on the existing self-telemetry TracerProvider, exported over OTLP:
  - one root span per target collection cycle, with a child span per collector (PR, issue, dashboard) and the metrics flush (`ForceFlush`) as a child span;
  - one span per unit of the target's worker pool: per repo for the PR collector and the issue title-filter listing, per GraphQL batch for the issue and dashboard collectors, always a child of its collector span;
  - one HTTP client span per GitHub round trip from the existing `otelhttp` transport, named `{method} {url.template}`, with `graphql.operation.type` and, when the document names it, `graphql.operation.name` on GraphQL calls, and `http.request.resend_count` on rate-limit retries;
  - one child span per primary or secondary rate-limit wait, with the pool and `reason=primary|secondary`.
- Errors: span status `Error` plus `error.type`. No exception span events: semconv 1.41.1, pinned in `registry/manifest.yaml`, deprecates exceptions on spans.
- The exporter never traces its own OTLP export.
- Sampling stays the SDK's `OTEL_TRACES_SAMPLER`/`OTEL_TRACES_SAMPLER_ARG`, default `parentbased_always_on`. The design quantifies spans per cycle for a large org and documents a ratio sampler.
- With traces off: no-op tracer, no exporter, no connection, GitHub transport and metric output unchanged.
- Weaver registry: the exporter's span names and new attributes declared as span groups; HTTP client spans by reference to the imported semconv `http.client` span. Live-check covers spans through `.weaver.toml` matchers (supported by weaver 0.28, verified).
- README "Self-telemetry": traces section. Helm chart: the self-telemetry values example enables traces.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `self-telemetry`: collection cycle traces, GitHub HTTP client spans, rate-limit wait spans, error recording, no self-tracing of OTLP export, sampling, no overhead when off.
- `weaver-schema`: span groups in the registry and span live-check.

## Impact

- `internal/collector`: `runner.go` (cycle, collector and flush spans), `pr.go`, `issue.go`, `batch.go` (unit spans), a tracer option.
- `internal/github`: `transport.go` (tracer provider for `otelhttp`, span name, `url.template` and GraphQL span attributes, resend count), `client.go` (rate-limit wait spans, tracer option), named batch queries in `internal/collector/batch.go`.
- `cmd/github-activity-exporter/main.go`: wiring of the tracer and tracer provider when traces are active.
- `registry/telemetry.yaml` (span groups, span imports, attributes), `internal/semconv/*.go` (generated), `.weaver.toml` (span matchers), `tests/integration` (span live-check, metric output identical with traces on and off).
- `README.md`, `charts/github-activity-exporter/examples/self-telemetry-values.yaml`.
- No change to product metric names, attributes or values, nor to the GitHub requests other than the operation name in the batched GraphQL queries.
