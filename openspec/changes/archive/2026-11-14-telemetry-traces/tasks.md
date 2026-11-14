# Tasks

## 1. Registry

- [x] 1.1 Declare the exporter's span groups and attributes in `registry/traces.yaml`; import the semconv `http.client` span and reference `graphql.operation.type` and `graphql.operation.name`
- [x] 1.2 Generate span name constants (`templates/go/spans.go.j2`) and run `make generate`
- [x] 1.3 Add span matchers to `.weaver.toml`

## 2. GitHub client

- [x] 2.1 `NewTransport` takes a TracerProvider for `otelhttp`: span name `{method} {url.template}`, `url.template` and GraphQL operation attributes on the span, no propagation
- [x] 2.2 `http.request.resend_count` on rate-limit retries
- [x] 2.3 `WithTracer`: `github_exporter.rate_limit.wait` spans for primary and secondary waits
- [x] 2.4 Operation names on the batched GraphQL queries; typed GraphQL errors for `error.type`
- [x] 2.5 Unit tests: client span names and attributes, no body read with traces off, wait spans

## 3. Collectors

- [x] 3.1 Cycle, collect and flush spans in the Runner (`WithCycleTracer`), outcome and failed repos
- [x] 3.2 Repo and batch spans from the closure context of the worker pool units
- [x] 3.3 No allocation with traces off (no-op tracer short-circuit)
- [x] 3.4 Unit tests: span tree at concurrency 4, failed units, discovery failure, flush not traced, metrics independent of traces
- [x] 3.5 Benchmark one cycle with traces off and on

## 4. Wiring

- [x] 4.1 `main.go` passes the tracer and TracerProvider only when traces are active

## 5. Live-check

- [x] 5.1 Span live-check: every span matched, both wait reasons, undeclared span reported
- [x] 5.2 OTLP and `/metrics` metric output identical with traces on and off

## 6. Docs

- [x] 6.1 README "Self-telemetry": traces section with toggles, sampler variables and volume
- [x] 6.2 Helm self-telemetry values example enables traces

## 7. Validation

- [x] 7.1 `go build ./...`, `go test -race ./...`, `make check-schema`, `make generate` without diff, live-checks, dashboard e2e
- [x] 7.2 One cycle against the mock GitHub to a local Jaeger: concurrency > 1, primary wait, secondary pause, failed repo
- [x] 7.3 One real-API cycle against `zextras`: spans per cycle
