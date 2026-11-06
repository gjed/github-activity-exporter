# Proposal

## Why

The metric naming contract ([#14](https://github.com/gjed/github-activity-exporter/issues/14), [#54](https://github.com/gjed/github-activity-exporter/pull/54)) is prose in `registry/manifest.yaml`, and `/metrics` is checked by a Go test, not by Weaver. Tooling cannot read the contract, and Weaver never sees the Prometheus exposition ([#57](https://github.com/gjed/github-activity-exporter/issues/57)).

## What Changes

- Registry (`registry/signals.yaml`): every metric, including the rate-limit gauges from [#16](https://github.com/gjed/github-activity-exporter/issues/16), declares `annotations.prometheus.name` (equal to the metric name) and `annotations.prometheus.type` (counter -> `counter`, updowncounter -> `gauge`, gauge -> `gauge`, histogram -> `histogram`).
- Registry policy: a Rego `after_resolution` policy in `policies/registry/` fails when a metric lacks the annotation, when `prometheus.name` differs from the metric name, or when `prometheus.type` does not follow from the instrument. `make check-schema` and `.github/workflows/registry-validate.yml` run `weaver registry check --v2 -r registry/ -p policies/registry/`.
- `/metrics` live-check: an integration test scrapes `/metrics`, converts the exposition to live-check JSON samples (`target_info` -> resource, `otel_scope_*` labels -> instrumentation scope, `_bucket`/`_sum`/`_count` -> one histogram data point) and runs `weaver registry live-check` on the file with a `live_check_advice` Rego policy in `policies/live_check/` that checks the exposed type against `annotations.prometheus.type`. The run uses its own weaver config, which downgrades the built-in `unexpected_instrument` and `unit_mismatch` findings to `information`. Negative tests: a suffixed name (`github.pr.automerged_total`) and a wrong exposed type both fail.
- OTLP live-check keeps the default advice policies. The all-metrics names test also fails on any violation other than a missing required attribute.
- Removed: `internal/exporter/naming_test.go`, and the prose contract in `registry/manifest.yaml` (the header points at the annotations and the policy; backend requirements stay in `README.md`).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `weaver-schema`: the naming contract is declared as annotations and enforced by a registry policy; CI runs the policy; live-check also verifies `/metrics`; the OTLP names test rejects any violation other than a missing required attribute.

## Impact

- Registry/policies: `registry/signals.yaml`, `registry/manifest.yaml`, new `policies/registry/`, new `policies/live_check/`, new `.weaver-prometheus.toml`.
- Build/CI: `Makefile` (`check-schema`), `.github/workflows/registry-validate.yml`.
- Tests: `tests/integration/` (new `/metrics` live-check tests, stricter OTLP names test), removed `internal/exporter/naming_test.go`.
- Code comment: `internal/exporter/provider.go` points at the annotations instead of the manifest.
- Generated files: none. The Go and dashboard templates do not read the annotations, so `make generate` produces no diff.
