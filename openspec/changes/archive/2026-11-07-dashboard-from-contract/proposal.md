# Proposal

## Why

`dashboards/github-activity.json` is generated from the registry, but the template hard-codes a naming rule that is wrong for the UTF-8 contract: dotted names in unquoted PromQL, no `_bucket` for histograms, counters and histograms plotted as raw gauges. Every panel returns no data on Prometheus 3.x and Mimir. Nothing checks that queries built on the telemetry work, so this breaks again on the next registry change ([#19](https://github.com/gjed/github-activity-exporter/issues/19)).

## What Changes

- Rewrite `templates/grafana/dashboard.json.j2`. Every query is built from the metric's `annotations.prometheus.name` and `annotations.prometheus.type`, never from the metric name pattern:
  - UTF-8 selector syntax: `{"github.pr.count", job=~"$job", "exporter.target"=~"$target", ...}`; UTF-8 label names quoted in `by()`.
  - `counter` -> `rate`/`increase`; `histogram` -> `histogram_quantile` over `<name>_bucket`; `gauge` -> raw value.
  - A panel references its metric by registry name; a missing metric or a type the panel cannot handle fails `make generate`.
- Variables: `datasource`, `job` (values of `job` on series whose job is `github-activity-exporter`, the `service.name`, optionally `<service.namespace>/` prefixed), `exporter.target`, `github.org`, `github.repo`, chained.
- Panels:
  - Pull requests: created/merged/closed rates, global and per repo; open PR count, oldest open PR age, open PRs by review status; close duration p50/p90; automerged rate.
  - Issues: open issue count, oldest open issue age.
  - GitHub API: rate limit remaining and reset per pool (`github.api.rate_limit.resource`).
  - Exporter self-metrics: scrape duration, API errors by endpoint, OTLP push errors.
  - Dependency Dashboard: its own row, Renovate queue by section and parse errors. Empty when the optional collector is off.
- CI e2e (`tests/e2e`, build tag `e2e`, new `Dashboard E2E` job, `make test-e2e`): run the exporter binary against a mock GitHub API with the dashboard collector on, push OTLP into Prometheus 3.x (`--web.enable-otlp-receiver`, `otlp.translation_strategy: NoTranslation`), then run every generated panel query and every variable query with variables substituted. Fail on any query error or empty result.
- `README.md`: dashboard section (variables, backend requirements, link to the e2e).

## Non-Goals

- Prometheus alerting/recording rules: none exist.
- Shipping the dashboard in the Helm chart: [#20](https://github.com/gjed/github-activity-exporter/issues/20) consumes the generated `dashboards/github-activity.json`.
- Registry or exporter behaviour changes.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `weaver-schema`: the dashboard requirement now states the query shape is derived from `annotations.prometheus`, the variables, and the panel set.
- `ci-checks`: a new requirement for the dashboard e2e query check on every PR.

## Impact

- `templates/grafana/dashboard.json.j2` (rewrite), `templates/grafana/weaver.yaml` (if the filter needs annotations), regenerated `dashboards/github-activity.json`.
- New `tests/e2e/` (mock GitHub API, Prometheus container, query runner), `Makefile` (`test-e2e`), `.github/workflows/ci.yml` (`Dashboard E2E` job, pinned `prom/prometheus` image).
- `README.md`: dashboard section. Overlaps with G1 (helm-chart) and G2 (telemetry-foundation); rebase on whichever merges first.
