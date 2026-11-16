# Proposal

## Why

Gauge panels aggregate `last_over_time(...[$__rate_interval])` over every series, and each exporter process writes its own series (`instance`, `service.instance.id`, `k8s.pod.name`, `host.name` differ). After a pod restart the old and new pod's series share one look-back window and the panels add them: [#133](https://github.com/gjed/github-activity-exporter/issues/133) shows `open=2082` at `[31m]` against `1041` at `[5m]`, for as long as `$__rate_interval` after every rollout, and the doubled stretch stays in the history.

## What Changes

- The gauge branch of the `expr` macro in `templates/grafana/_macros.j2` wraps `last_over_time(...)` in an inner `max by (<identity>)` before the panel's own `sum`/`max`/`min by (...)`. Identity is `job`, plus `exporter.target` when the query is per target, plus every attribute the registry declares on the metric. The list is built from the registry, not per panel.
- Every gauge panel of both generated dashboards changes through the macro: Open PRs by state, Open PRs by review status, Oldest open PR age, Issues by state, Oldest open issue age, Renovate queue by section, Renovate parse errors, Rate limit remaining, Time until rate limit reset, Go memory (used, heap goal, limit) and Goroutines. `rate()` and `histogram_quantile()` queries do not change.
- The macro's header comment and `docs/dashboards.md` describe the new gauge shape.
- A new e2e test runs two exporter processes against one Prometheus and checks that every gauge panel equals the per-series maximum of the panel over each process alone.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `weaver-schema`: the "Grafana dashboard generated from schema" requirement states the gauge query shape, collapsed per metric identity before the panel aggregation, and gains a scenario for two exporter processes writing the same series.

## Impact

- `templates/grafana/_macros.j2` (gauge branch of `expr` and its header comment only).
- Generated `dashboards/github-activity.json` and `dashboards/github-activity-exporter.json` (via `make generate`).
- `tests/e2e/dashboard_e2e_test.go` (new test).
- `docs/dashboards.md`.
- No exporter code, registry or chart change. The Helm chart ships the regenerated dashboards in its next release.
