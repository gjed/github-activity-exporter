# Design

## Context

The registry declares `annotations.prometheus.{name,type}` on every metric and a registry policy keeps them in sync with the instrument ([#57](https://github.com/gjed/github-activity-exporter/issues/57)). The dashboard template ignores them. The target deployment is the Helm chart ([#20](https://github.com/gjed/github-activity-exporter/issues/20)) into a Grafana backed by Prometheus 3.x or Mimir, with UTF-8 names and the OTLP `NoTranslation` strategy. Under that strategy a `counter` keeps its name with no `_total`, and a `histogram` is stored as classic `<name>_bucket`/`_sum`/`_count` series. Prometheus derives `job` from `service.namespace/service.name`, or `service.name` alone (`github-activity-exporter`).

## Goals / Non-Goals

**Goals:**

- Every query shape follows from the annotations, so a registry change either regenerates a working dashboard or fails `make generate`.
- A CI check that runs every generated query against a real Prometheus 3.x fed by the real exporter binary.

**Non-Goals:**

- Running Grafana in CI. The e2e checks PromQL against the backend; Grafana-side interpolation is checked once by hand (see Risks).
- Alerting/recording rules (none exist); Helm packaging of the dashboard (#20).

## Decisions

- **Query macros keyed on `annotations.prometheus.type`.** The template defines one macro per shape: `counter_rate(metric, by)`, `hist_quantile(metric, q, by)`, `gauge(metric, by)`. Each reads the name from `metric.annotations.prometheus.name` and checks `annotations.prometheus.type`; the wrong type raises a template error. Panels are a declared list in the template that reference metrics by registry name through a lookup over `ctx`; a missing name raises. The template never inspects name prefixes. Alternative: loop over all metrics and pick a panel type per metric. Rejected: the requested panels combine metrics (created/merged/closed in one panel, p50 and p90 of one histogram), which a per-metric loop cannot express.
- **Selector.** `{"<name>", job=~"$job", "exporter.target"=~"$target", "github.org"=~"$org", "github.repo"=~"$repo"}`. The `github.org`/`github.repo` matchers are added only when the metric declares those attributes (self-metrics and rate-limit gauges do not); the template reads `metric.attributes`. Counter: `sum by (<labels>) (rate(<sel>[$__rate_interval]))`. Histogram: `histogram_quantile(<q>, sum by (le, <labels>) (rate({"<name>_bucket", ...}[$__rate_interval])))`. Gauge: `sum by (<labels>) (<sel>)`, `max` for ages and the reset timestamp, `min` for remaining rate limit. UTF-8 label names in `by()` are quoted. `github_exporter.otlp.errors` drops the `$target` filter: one OTLP exporter pushes for all targets and labels its errors `exporter.target="default"`, so `$target` never matches it.
- **Legends are not quoted.** Grafana renders `{{github.repo}}` by looking up the text between the braces as the label key (`renderLegendFormat` in `packages/grafana-data/src/utils/legend.ts`); `{{"github.repo"}}` would look up a key with quotes and print it literally. Legends use the bare dotted name.
- **Variables.** Query variables of type label values, chained: `job` from series with `job=~"(.+/)?github-activity-exporter"`; `exporter.target` filtered by `$job`; `github.org` by `$job` and `$target`; `github.repo` by all three. Multi-value with `All` (`.*`). Variable names are `job`, `target`, `org`, `repo`, labelled with the attribute names, because Grafana variable names cannot contain dots. The query is a classic `label_values(<selector>, <label>)` string: Grafana escapes a dotted label in the API path (`/api/v1/label/U__github_2e_org/values`), and Prometheus 3.x accepts it.
- **Rows.** Pull requests, Issues, GitHub API, Exporter self-metrics, Dependency Dashboard. The Dependency Dashboard row is last, so the panels that show no data when the collector is off are grouped together.
- **e2e as a Go test, build tag `e2e`, in `tests/e2e`.** Same toolchain as the integration suite, no new dependency. Steps:
  1. Start `prom/prometheus:v3.16.0` (pinned Go const; Renovate's regex managers only cover workflow files, so bumps are manual) with `--web.enable-otlp-receiver` and a config setting `otlp.translation_strategy: NoTranslation`.
  1. Start a mock GitHub API (`httptest`): `GET /user` for the PAT check, org repo listing, the GraphQL PR query, issue listing including a Dependency Dashboard issue, `X-RateLimit-*` headers with `X-RateLimit-Resource` `core` and `graphql`. Every request after the first cycle returns a fresh created, merged (no approving review, so automerged) and closed PR, so counters and the histogram move every cycle. One repo's issue listing fails once, so `github_exporter.api.errors` has a series.
  1. Put a reverse proxy in front of Prometheus' OTLP endpoint that rejects the first push with HTTP 400 (not retried by the OTLP exporter), so `github_exporter.otlp.errors` has a series.
  1. Build and run the exporter binary with a temp config: `base_url` = mock, `GITHUB_TOKEN`, a short `collection_interval`, `renovate_dashboard.enabled: true`, OTLP HTTP to the proxy.
  1. Wait until Prometheus has enough samples for a rate (several cycles), then parse `dashboards/github-activity.json`, resolve every variable query through the Prometheus API, substitute `$job`/`$target`/`$org`/`$repo` with `.*`-free concrete values, `$__rate_interval` with a fixed window, and run every panel target as an instant query.
  1. Fail on any error, on an empty vector, or on a vector whose samples are all NaN (a quantile over zero rate renders nothing in Grafana, so it is empty in practice). The failure names the panel title, `refId`, the expanded query and the response.
- **CI job.** `Dashboard E2E` in `.github/workflows/ci.yml` on every PR and push to main, `go test -tags e2e -count=1 ./tests/e2e/...`. `make test-e2e` runs the same.

## Risks / Trade-offs

- [The e2e does not run Grafana, so variable and legend interpolation are not covered in CI] -> One manual check against a local Grafana container before the PR, recorded in the PR body. Adding Grafana to CI would cover `/api/ds/query` panel queries but not variable resolution, which is frontend code.
- [Timing flakiness: rate needs two samples in the window] -> Poll Prometheus until every metric has at least two samples, with a deadline, instead of sleeping.
- [Mock drifts from the real GitHub API] -> The mock serves only what the collectors request; REST responses use go-github types; any request the mock does not handle fails the test.
- [Prometheus 3.x defaults change] -> The image is pinned; a bump is a deliberate edit and the e2e runs on that PR.
