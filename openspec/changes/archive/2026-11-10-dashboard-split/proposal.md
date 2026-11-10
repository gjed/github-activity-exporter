# Proposal

## Why

`dashboards/github-activity.json` mixes the GitHub KPIs with the exporter's own instrumentation ([#90](https://github.com/gjed/github-activity-exporter/issues/90)). Readers of PR and issue activity get cycle durations, error counters and runtime panels they do not need, the opt-in self-telemetry panels show empty rows when it is off, and the KPI variables `$job` and `$target` resolve from a self-metric (`github_exporter.scrape.duration_count`). The two audiences also need separate Grafana folders and permissions.

## What Changes

- `dashboards/github-activity.json` keeps only `github.*` and `renovate.*` panels. Its `job` and `exporter.target` variables resolve from the product series of the dashboard's own metrics, so a target with only some collectors enabled still resolves.
- New generated `dashboards/github-activity-exporter.json` for the exporter: collection cycles (`scrape.duration`, `scrape.overruns`), GitHub API errors, OTLP push errors, rate-limit headroom per pool, and the Go runtime and GitHub HTTP client panels from [#74](https://github.com/gjed/github-activity-exporter/issues/74). It covers every `github_exporter.*` registry metric.
- Both dashboards are rendered by `make generate` from shared macros, keep the `$min_step` constant variable and the same `job`/`exporter.target` filtering. Generation fails if the KPI dashboard queries a non-product metric or the exporter dashboard misses a `github_exporter.*` metric.
- Helm chart ships both dashboards, each toggled and labelled separately: `dashboard.kpi.*` and `dashboard.exporter.*`. `dashboard.minStep` applies to both. **BREAKING** (chart values, 0.x): `dashboard.enabled`, `dashboard.labels` and `dashboard.annotations` move to `dashboard.kpi.*`; rendering fails with a migration message when the old keys are set.
- The dashboard e2e runs every query of every generated dashboard, with self-telemetry metrics on and off.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `weaver-schema`: the generated Grafana dashboard requirement becomes two generated dashboards (KPI and exporter) with their variable sources and coverage rules.
- `helm-chart`: the Grafana dashboard ConfigMap requirement becomes two toggleable ConfigMaps with their own labels and annotations.
- `ci-checks`: the chart check triggers on every generated dashboard; the dashboard e2e covers every generated dashboard with self-telemetry metrics on and off.

## Impact

- `templates/grafana/` (`weaver.yaml`, `dashboard.json.j2`, new exporter template and shared macros), regenerated `dashboards/*.json`.
- `charts/github-activity-exporter/` (`values.yaml`, `templates/dashboard.yaml`, new `dashboards/` symlink, README, the GitHub App example).
- `tests/e2e/` (`TestDashboardQueries`, `TestDashboardContinuity`, exporter harness telemetry toggle).
- `.github/workflows/chart.yml` path filter, `README.md` dashboard section.
- No Go code or registry change.
