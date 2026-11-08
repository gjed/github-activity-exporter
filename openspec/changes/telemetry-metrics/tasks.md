# Tasks

## 1. Registry contract

- [ ] 1.1 Add `registry/telemetry.yaml`: import the nine `go.*` metrics, `http.client.request.duration` and `http.client.request.body.size` from the `otel` dependency, one refinement each with `annotations.prometheus` and `unref` for attributes never attached; verify `make check-schema` passes
- [ ] 1.2 Update `policies/registry/prometheus.rego` to require the annotation on a refinement of every metric and check name/type on every annotated refinement; add a bad-registry fixture under `tests/integration/testdata/` with an import lacking a refinement and assert the violation in `registry_policy_test.go`; verify `go test -tags integration -run Policy ./tests/integration/...`
- [ ] 1.3 Exclude imported groups from `templates/go/weaver.yaml`; switch `templates/grafana/` to `--v2` (`.refinements.metrics`, v2 keys) in the template, `weaver.yaml` and the `Makefile`; verify `make generate` leaves `internal/semconv/*.go` and the existing dashboard queries unchanged

## 2. Runtime metrics

- [ ] 2.1 Add `go.opentelemetry.io/contrib/instrumentation/runtime` v0.72.0; in `internal/telemetry` attach `runtime.NewProducer()` to both readers and call `runtime.Start` when metrics are active; tests that an active provider yields every `go.*` metric (with `go.memory.limit` only under a memory limit) and an inactive one none; verify `go test ./internal/telemetry/...`

## 3. HTTP client metrics

- [ ] 3.1 Add `otelhttp` v0.72.0; add `NewTransport(mp)` with `routeTransport` (route table, `url.template`, `error.type` for 4xx/5xx, no-op tracer and propagator), `WithBaseTransport` on `Client`, and an HTTP client on the PAT and App authenticators for ping and token exchange; verify `go build ./...`
- [ ] 3.2 Transport tests in `internal/github` with `httptest` and a `ManualReader`: 401 reaches the auth hook and is recorded; rate-limit pause and gauges unchanged and the paused request's duration excludes the wait; REST, GraphQL, ping and token exchange `url.template` (github.com and GHES roots); no repo name in attributes; unknown path without `url.template`; 5xx `error.type`; nil provider returns `http.DefaultTransport`; verify `go test ./internal/github/...`
- [ ] 3.3 Wire in `main.go`: build the transport from `tel.MeterProvider()` only when `tel.Active(telemetry.SignalMetrics)`, pass it to the client and authenticators; verify `go build ./... && go test ./cmd/...`

## 4. Live-checks

- [ ] 4.1 `policies/live_check/prometheus.rego`: cover imported metrics (fallback to the instrument-derived type when `registry_group` has no annotation) after checking what live-check passes as `registry_group`
- [ ] 4.2 `tests/integration`: run the OTLP and `/metrics` live-checks with self-telemetry metrics on (GitHub requests through a mock server and the instrumented client) and off; on: `go.*`/`http.client.*` seen, no violation, wrong exposed type on an imported metric detected; off: no `go.*`/`http.*`; the registry-name enumeration includes imported metrics; verify `make test-integration`

## 5. Dashboard

- [ ] 5.1 Add the "Exporter internals" row to `templates/grafana/dashboard.json.j2` (memory by type with goal and limit, allocation rate, goroutines, GitHub API latency p50/p90 by `url.template` and status) and `per_target` on the quantile shape; `make generate`; verify the diff only adds the row
- [ ] 5.2 Turn self-telemetry metrics on in the e2e config with a short `OTEL_METRIC_EXPORT_INTERVAL`; verify `make test-e2e` runs every query, new ones included, with data

## 6. Chart and docs

- [ ] 6.1 Add `charts/github-activity-exporter/examples/self-telemetry-values.yaml` enabling only self-telemetry metrics; verify `helm template` renders it and the ConfigMap holds the `telemetry` section
- [ ] 6.2 README: runtime and GitHub HTTP client metrics, activation, attributes and `url.template` routes; markdown lint on changed files

## 7. Validation

- [ ] 7.1 `go build ./... && go test ./... && make check-schema && make generate` with no unexpected diff; integration live-checks on and off; dashboard e2e

## Workflow follow-up

- `openspec archive telemetry-metrics` before the final commit, in the same PR.
- PR body: `Closes #74`, assignee `gjed`; re-check `gh pr view --json state` before every push.
