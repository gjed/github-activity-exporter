# Design

## Context

See proposal.md for motivation. On current main (`3db7734`):

- `telemetry.New` (`internal/telemetry/telemetry.go`) already builds an SDK `TracerProvider` with an OTLP batch exporter when traces are active, sets it as the global provider, and leaves the sampler to the SDK environment (`OTEL_TRACES_SAMPLER`, default `parentbased_always_on`). `Telemetry.Tracer()` returns a tracer with the exporter's scope and version; inactive traces get the no-op provider. Nothing creates spans yet.
- `Runner.runOnce` (`internal/collector/runner.go`) calls `collect` then `flushMetrics` (`Provider.ForceFlush`). `collect` discovers repos (`Discoverer.Repos`: cached, except the first call, which pages `/orgs/{org}/repos`) and runs the three collectors in their own goroutines.
- One `Limiter` per target (`limiter.go`, built in `main.go` from `t.Concurrency()`) runs units through `each`, each `fn(i)` in its own goroutine. Units: a PR repo (`pr.go`, open scan plus event scan), an issue title-filter repo (`issue.go`), a GraphQL batch with its per-repo fallbacks (`batch.go` `eachBatch`, used by `issue.go` and `dashboard.go`).
- `NewTransport` (`internal/github/transport.go`) wraps `routeTransport` in `otelhttp` with the self-telemetry MeterProvider and an explicit no-op TracerProvider and empty propagator; it is `http.DefaultTransport` when self-telemetry metrics are off. It sits under `rateLimitTransport` and is also the authenticators' transport.
- `rateLimitTransport.RoundTrip` (`internal/github/client.go`) waits on the target pause (`waitIfTargetPaused`, secondary limits) and the pool pause (`waitIfPaused`, primary limits) before each attempt, and retries a rate-limited response up to 3 times.
- The OTLP exporters (`otlpmetrichttp/grpc`, `otlptracehttp/grpc`, `otlploghttp/grpc`) use their own HTTP and gRPC clients, which no tracer instruments.

## Goals / Non-Goals

**Goals:**

- One trace per target cycle that shows the time spent per collector, per unit of the worker pool, per GitHub round trip and per rate-limit wait, with failed units marked.
- Bounded span names; semconv names and attributes where a convention exists.
- No change in behavior, metric output or cost with traces off.

**Non-Goals:**

- Logs and log-to-trace correlation ([#76](https://github.com/gjed/github-activity-exporter/issues/76)), including exceptions as log records.
- Trace context propagation to GitHub.
- Spans for work outside a cycle: startup credential check, background discovery refresh, App token exchange (see Decisions).
- Tail sampling or any sampler beyond the SDK's environment variables.

## Decisions

### Span tree

```text
github_exporter.cycle                      root, one per target cycle
├── POST /graphql, GET /orgs/{org}/repos   first cycle only: discovery requests
├── github_exporter.collect  (pr)
│   └── github_exporter.repo               one per repo
│       ├── github_exporter.rate_limit.wait
│       └── POST /graphql                  one per page and per attempt
├── github_exporter.collect  (issue)
│   └── github_exporter.batch              one per batch (or .repo with title patterns)
│       └── POST /graphql                  the batch query, then per-repo fallbacks
├── github_exporter.collect  (dashboard)
│   └── github_exporter.batch
└── github_exporter.flush
```

All internal spans are `SpanKind` internal, their names constants declared in the registry. Each unit span starts in `fn(i)` from the `ctx` the closure captures, which carries the collector span, so it is a child of the cycle even though it runs in its own goroutine. Units the limiter never starts (cancelled cycle) get no span.

| Span                              | Attributes                                                                                                                                     | Status `Error` when            |
| --------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------ |
| `github_exporter.cycle`           | `exporter.target`, `github_exporter.cycle.repos` (when discovery succeeded), `github_exporter.cycle.outcome` (`success`, `partial`, `failure`) | discovery failed (`failure`)   |
| `github_exporter.collect`         | `github_exporter.collector` (`pr`, `issue`, `dashboard`), `github_exporter.failed_repos`                                                       | any repo failed                |
| `github_exporter.repo`            | `github.org`, `github.repo`                                                                                                                    | the repo failed                |
| `github_exporter.batch`           | `github_exporter.batch.size`, `github_exporter.failed_repos`                                                                                   | any repo of the batch failed   |
| `github_exporter.flush`           | none                                                                                                                                           | `ForceFlush` returned an error |
| `github_exporter.rate_limit.wait` | `github.api.rate_limit.resource` (the request's pool), `github_exporter.rate_limit.reason` (`primary`, `secondary`)                            | the wait was cancelled         |

`outcome` is `partial` when discovery succeeded and at least one repo failed in any collector, `success` otherwise. `exporter.target` is on the root only: every other span of the trace belongs to it. A wait span covers the actual wait, so the wait duration is the span's duration; no duration attribute. It is created only when there is something to wait for, so an unpaused request costs no span. A secondary limit shows as the 403 or 429 client span (status `Error`, `error.type` the status code) followed by `rate_limit.wait` spans with `reason=secondary` in that request and in every other request of the target held by the pause.

`exporter.target`, `github.org`, `github.repo` and `github.api.rate_limit.resource` are existing registry attributes. Repo names are attribute values on `github_exporter.repo` spans, never in a span name. Trace attribute values do not create series, so the metric cardinality rule does not apply to them.

### HTTP client spans

`NewTransport(baseURL, mp, tp)` gets the TracerProvider and passes it to `otelhttp`; it stays `http.DefaultTransport` when both providers are nil. There is still one `otelhttp` layer, so one span per round trip (per attempt, as semconv asks for resends), under the rate-limit transport and its waits.

- Span name `{method} {url.template}` through `otelhttp.WithSpanNameFormatter` using `routeTransport.template`; `{method}` alone for a path outside the known routes, as the HTTP span convention allows. `otelhttp`'s default is `HTTP {method}`.
- `url.template` is set on the span as well as on the metric labeler. `url.full` stays, as `otelhttp` sets it: it is Required by the `http.client` span convention, and GitHub URLs carry no credentials (the token is a header).
- GraphQL: `graphql.operation.type` and `graphql.operation.name` (semconv 1.41.1 `graphql` registry) on spans to the GraphQL path, read from the request body's `query` (first keyword, `query` for the `{ ... }` shorthand; the name when present). The body is read through `GetBody` and only when the span is recording. The two batched queries get operation names (`IssueCountBatch`, `DashboardBatch`); `githubv4` typed queries (PR scans, per-repo fallbacks) are anonymous, so they carry the type only, as the convention says for an unnamed operation.
- `http.request.resend_count` on attempts after the first, passed from `rateLimitTransport` through the request context.
- `error.type` and status `Error` for transport errors and 4xx/5xx come from `otelhttp`.
- Propagator stays empty: GitHub does not take part in the trace, and a `traceparent` header would hand trace IDs to a third party.

### Errors

Span status `Error` with a description and `error.type`. `error.type` is the GraphQL error type when GitHub returned one for the repo in a batched query (`NOT_FOUND`, `FORBIDDEN`), the HTTP status code for a go-github `ErrorResponse`, `_OTHER` otherwise; a batch or a collector takes the type of its first failed repo. Bounded in every case. `githubv4` typed queries (PR scans, per-repo fallbacks) return GraphQL errors without their type, so they are `_OTHER`; their HTTP client span still carries the status code of a non-200 response.

No `RecordError`/`AddEvent`. In semconv 1.41.1 exceptions on spans are Deprecated in favour of exceptions in logs, and `exception.escaped` is deprecated because handled exceptions should not be recorded; every error here is handled (the repo fails, the cycle continues). Following the span-events-to-logs migration, exceptions belong to the logs signal, which is [#76](https://github.com/gjed/github-activity-exporter/issues/76).

### Wiring

- `collector.WithTracer(trace.Tracer)` for the Runner, `collector.WithTracer`-style `Option` for the issue and dashboard collectors, `PRCollectorConfig.Tracer`; nil means the no-op tracer.
- `ghclient.WithTracer(trace.Tracer)` for the wait spans.
- `main.go` passes `tel.Tracer()` and `tel.TracerProvider()` only when `tel.Active(SignalTraces)`; otherwise nil. With metrics on and traces off, `otelhttp` keeps its no-op TracerProvider, as today.

The tracer is passed explicitly rather than taken from the global provider, like the MeterProvider of [#74](https://github.com/gjed/github-activity-exporter/issues/74).

### No tracing of the OTLP export

The OTLP exporters never get a tracer and none of their clients is wrapped. `github_exporter.flush` times `ForceFlush` with no child spans. A unit test asserts that a flush to a test OTLP endpoint adds no span other than `github_exporter.flush`. The span batch processor's own exports are not traced either.

### Spans outside a cycle

The startup credential check, the background discovery refresh (`Discoverer.Start`) and the App installation token exchange (`oauth2.TokenSource.Token` has no context, so it runs on `context.Background`) send requests outside any cycle span. Their client spans are roots of their own one-span traces. Giving them parent spans needs a discovery span and a context-aware token source; not part of this issue.

### Sampling and volume

The SDK applies `OTEL_TRACES_SAMPLER`; default `parentbased_always_on`. The cycle span is the only root of a cycle, so the sampler decides once per cycle and a trace is kept or dropped whole.

Spans per cycle, steady state (after the first cycle), Dependency Dashboard enabled, no title patterns, batch size 20:

| Source                                  | Spans for `R` repos | `zextras`, R = 392 |
| --------------------------------------- | ------------------- | -----------------: |
| cycle, 3 collectors, flush              | 5                   |                  5 |
| PR repo units                           | R                   |                392 |
| PR client spans (open scan, event scan) | ≥ 2R                |               ≥784 |
| issue and dashboard batches             | 2⌈R/20⌉             |                 40 |
| batch client spans (no fallback)        | 2⌈R/20⌉             |                 40 |
| total                                   | ≈ 3R + 4⌈R/20⌉ + 5  |            ≈ 1,261 |

Repos with more than 100 open PRs, more label pages or fallbacks add client spans; rate-limit waits add wait spans. The first cycle has no event scan (R fewer) and adds the discovery pages: measured on `zextras`, 877 spans (392 repo, 40 batch, 433 GraphQL, 6 discovery pages, 1 owner lookup, 5 cycle, collector and flush). Concurrency changes the shape of the tree, not the count. At the default `collection_interval` of 5 minutes, about 1,270 spans per cycle is about 366,000 spans a day per target. For large orgs:

```yaml
env:
  - name: OTEL_TRACES_SAMPLER
    value: parentbased_traceidratio
  - name: OTEL_TRACES_SAMPLER_ARG
    value: "0.1"   # one cycle in ten, about 37,000 spans a day at R = 392
```

### Registry and live-check

Span groups (`definition/2` `spans`) for the six internal spans and the new attributes go in `registry/telemetry.yaml` with the self-telemetry metrics. HTTP client spans are the imported semconv `http.client` span (`imports.spans: [span.http.client]`), and an attribute group references `graphql.operation.type` and `graphql.operation.name` from the dependency.

Weaver 0.28 live-check checks spans once a matcher in `.weaver.toml` pairs them with a span type (verified against 0.28.0: a matched span reports `kind_mismatch`, attributes outside its signal and missing required attributes; an unmatched span reports each attribute as unknown). Matchers: `kind == "client"` to `http.client` plus the GraphQL attribute group, `name == "<constant>"` to each internal span. A matcher's `signal` must be a span type, so the GraphQL attributes come in as a permitted attribute group rather than a span refinement. The integration test runs cycles against the fixture GitHub client with traces exported to live-check and fails on violations and on any span sample with no matched signal. A Go unit test also asserts the span tree (parents, names, attributes) at concurrency 4 with an in-memory exporter, which live-check cannot see.

### Overhead when off

With traces inactive the Runner, collectors and client hold the no-op tracer: `Start` returns a non-recording span, attributes are built only behind `span.IsRecording()`, and the GraphQL body is never read. The transport is unchanged. A benchmark of one cycle on the collector fixtures, traces off versus on (in-memory exporter), is reported in the PR.

## Alternatives considered

- Rate-limit waits as span events on the unit span: a span event has no duration, so the waterfall would not show where the time went. Child spans do, and live-check checks them by name.
- Taking the tracer from the parent span's provider in each collector (`trace.SpanFromContext(ctx).TracerProvider()`), as `otelhttp` does: no option plumbing, but the instrumentation scope version would be lost. Explicit options match the MeterProvider wiring.
- One span per repo in the batched collectors: the batch is the unit of work and of the request, so a per-repo span would have no duration of its own.
- Recording errors with `RecordError`: deprecated in the pinned semconv for spans; see Errors.

## Risks / Trade-offs

- Volume on large orgs → documented ratio sampler; traces are opt-in.
- `otelhttp` span ends when the response body is closed; a caller that never closes it leaves the span open. go-github and `githubv4` close bodies; `QueryRaw` defers `Close`.
- Reading the GraphQL body for the operation name costs one JSON decode of the request per call → only when the span is recording.
- Weaver span live-check is `definition/2`, not yet stable → pinned to 0.28; the Go span-tree test does not depend on it.
