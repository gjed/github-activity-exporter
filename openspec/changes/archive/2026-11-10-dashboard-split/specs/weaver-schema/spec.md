## MODIFIED Requirements

### Requirement: Grafana dashboard generated from schema

The system SHALL generate two Grafana dashboards from the Weaver registry using Jinja2 templates:

- `dashboards/github-activity.json` (KPI dashboard): only metrics in the `github.*` and `renovate.*` namespaces. It SHALL define the variables `datasource`, `job`, `exporter.target`, `github.org`, `github.repo` and `min_step`, and no variable SHALL resolve from a `github_exporter.*` or self-telemetry metric.
- `dashboards/github-activity-exporter.json` (exporter dashboard): every `github_exporter.*` metric of the registry and the self-telemetry runtime and HTTP client metrics. It SHALL define the variables `datasource`, `job`, `exporter.target` and `min_step`.

Every panel query SHALL be built from the metric's `annotations.prometheus.name` and `annotations.prometheus.type` in UTF-8 PromQL syntax, with names and UTF-8 label names quoted; for an imported metric the annotation comes from its refinement. Both dashboards SHALL filter every query by `job` and, for metrics recorded per target, by `exporter.target`.

#### Scenario: Dashboard regenerated on schema change

- **WHEN** a metric definition changes in the registry and `make generate` is run
- **THEN** the dashboard JSON that uses the metric is updated to reflect the new metric name or attribute

#### Scenario: Query shape follows the Prometheus type

- **WHEN** `make generate` renders a panel for a metric
- **THEN** a `counter` is queried through `rate` or `increase`, a `histogram` through `histogram_quantile` over `<name>_bucket`, a `gauge` through `last_over_time`, every window is `$__rate_interval`, and the metric name comes from `annotations.prometheus.name`

#### Scenario: Panel Min interval from a variable

- **WHEN** `make generate` renders either dashboard
- **THEN** every panel's Min interval is `$min_step`, a constant variable whose default is at least twice the default `export.interval` and twice the default `collection_interval`

#### Scenario: Continuous panels with a long collection interval

- **WHEN** the exporter runs with `collection_interval: 15m` and the default `export.interval`
- **THEN** every `rate()` panel of both dashboards has a value at every step of the dashboard's default Min interval, though that interval is shorter than `collection_interval`

#### Scenario: Panel references an unknown metric

- **WHEN** a template references a metric the registry does not define, or a panel needs a Prometheus type the metric does not have
- **THEN** `make generate` fails

#### Scenario: Dashboard scope enforced at generation

- **WHEN** the KPI template queries a metric outside `github.*` and `renovate.*`, or the exporter template leaves a `github_exporter.*` registry metric without a panel
- **THEN** `make generate` fails

#### Scenario: KPI variables without self-metrics

- **WHEN** a target exports product metrics and no `github_exporter.*` or self-telemetry series exists for it
- **THEN** the KPI dashboard's `job` and `exporter.target` variables still list it

#### Scenario: KPI variables with some collectors off

- **WHEN** a target runs without the Dependency Dashboard collector
- **THEN** the KPI dashboard's `job` and `exporter.target` variables list it from its PR and issue series

#### Scenario: Dashboard contains panels for key metrics

- **WHEN** the KPI dashboard is imported into Grafana
- **THEN** it contains panels for: PR created/merged/closed rates globally and per repo, open PR count, oldest open PR age, open PRs by review status, PR close duration p50/p90, automerged rate, open issue count, oldest open issue age, and a separate Dependency Dashboard row with the Renovate queue by section and parse errors

#### Scenario: Exporter dashboard contains panels for the exporter

- **WHEN** the exporter dashboard is imported into Grafana
- **THEN** it contains panels for: collection cycle duration p50/p90 and overruns per target, GitHub API errors per target and endpoint, OTLP push errors, rate limit remaining and time until reset per target and pool, and a separate self-telemetry row with runtime memory by type, heap goal and limit, heap allocation rate, goroutine count, and GitHub API request latency p50/p90 per `url.template` and `http.response.status_code`

#### Scenario: Dependency Dashboard row without the collector

- **WHEN** no target enables the Renovate Dependency Dashboard collector
- **THEN** only the KPI dashboard's Dependency Dashboard row shows no data

#### Scenario: Exporter internals row without self-telemetry metrics

- **WHEN** self-telemetry metrics are inactive
- **THEN** every KPI dashboard panel shows data, and only the exporter dashboard's self-telemetry row shows no data
