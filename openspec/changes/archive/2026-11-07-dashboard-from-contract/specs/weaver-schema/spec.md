## MODIFIED Requirements

### Requirement: Grafana dashboard generated from schema

The system SHALL generate a `dashboards/github-activity.json` Grafana dashboard from the Weaver registry using a Jinja2 template. Every panel query SHALL be built from the metric's `annotations.prometheus.name` and `annotations.prometheus.type` in UTF-8 PromQL syntax, with names and UTF-8 label names quoted. The dashboard SHALL define the variables `datasource`, `job`, `exporter.target`, `github.org` and `github.repo`.

#### Scenario: Dashboard regenerated on schema change

- **WHEN** a metric definition changes in the registry and `make generate` is run
- **THEN** the dashboard JSON is updated to reflect the new metric name or attribute

#### Scenario: Query shape follows the Prometheus type

- **WHEN** `make generate` renders a panel for a metric
- **THEN** a `counter` is queried through `rate` or `increase`, a `histogram` through `histogram_quantile` over `<name>_bucket`, a `gauge` through `last_over_time`, every window is at least twice the default `collection_interval`, and the metric name comes from `annotations.prometheus.name`

#### Scenario: Panel references an unknown metric

- **WHEN** the template references a metric the registry does not define, or a panel needs a Prometheus type the metric does not have
- **THEN** `make generate` fails

#### Scenario: Dashboard contains panels for key metrics

- **WHEN** the dashboard is imported into Grafana
- **THEN** it contains panels for: PR created/merged/closed rates globally and per repo, open PR count, oldest open PR age, open PRs by review status, PR close duration p50/p90, automerged rate, open issue count, oldest open issue age, GitHub API rate limit per pool, scrape duration, API errors and OTLP push errors, and a separate Dependency Dashboard row with the Renovate queue by section and parse errors

#### Scenario: Dependency Dashboard row without the collector

- **WHEN** no target enables the Renovate Dependency Dashboard collector
- **THEN** only the Dependency Dashboard row shows no data
