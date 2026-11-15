# Proposal

## Why

`renovate.json` holds `github.com/prometheus/common` below v0.73.0 ([#105](https://github.com/gjed/github-activity-exporter/pull/105)), security fixes included. v0.73.0 ([prometheus/common#1008](https://github.com/prometheus/common/pull/1008)) takes the name escaping only from the `Accept` clause that matched, and expfmt only negotiated Prometheus text 0.0.4, so the Prometheus 3.x default scrape got `github_pr_count`. v0.74.0 adds Prometheus text format 1.0.0 negotiation ([prometheus/common#1035](https://github.com/prometheus/common/pull/1035)), so the hold can go ([#109](https://github.com/gjed/github-activity-exporter/issues/109)).

## What Changes

- Bump `github.com/prometheus/common` to v0.74.0 and remove its Renovate `packageRule`.
- `/metrics` serves text format 1.0.0 to a scraper that offers it. The Prometheus 3.x default scrape still gets `github.pr.count`, as before the hold.
- A scrape whose `Accept` header offers only OpenMetrics now gets underscore-escaped names (`github_pr_count`): `/metrics` serves no OpenMetrics, the clause carrying `escaping=allow-utf-8` matches no served format, and the fallback uses the default escaping. Before, it got `github.pr.count`. Such a scraper needs `PrometheusText1.0.0` in `scrape_protocols`. Requests with no `Accept` header or with `text/plain;version=0.0.4` are unchanged (`github_pr_count`).
- OTLP export is unchanged: `expfmt` is only used by the `/metrics` handler.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `metric-export`: `/metrics` name escaping follows the matching `Accept` clause; OpenMetrics-only and legacy scrapes get underscore-escaped names.
- `weaver-schema`: the `/metrics` live-check scrapes with the Prometheus 3.x default `Accept` header and with text 1.0.0 alone.

## Impact

- `go.mod`, `go.sum`, `renovate.json`.
- `tests/integration/prometheus_live_check_test.go`: the `openmetrics` case uses the Prometheus v3.16.0 default `Accept` header.
- `docs/metrics.md`: the OpenMetrics-only case.
