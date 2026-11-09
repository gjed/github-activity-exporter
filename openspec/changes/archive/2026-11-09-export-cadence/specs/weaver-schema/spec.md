## MODIFIED Requirements

### Requirement: Go constants generated from schema

The system SHALL generate a `internal/semconv/` Go package from the Weaver registry containing typed constants for all metric names and attribute key strings the registry defines itself, and, for every enum attribute, a constant per member value and a list of all member values. Metrics and attributes imported from the OTel semantic conventions dependency SHALL NOT get generated constants; the instrumentation libraries that record them own their names.

#### Scenario: Generated file is up to date

- **WHEN** `make generate` is run
- **THEN** `internal/semconv/metrics.go` and `internal/semconv/attributes.go` are regenerated and match the current registry

#### Scenario: Enum value list follows the registry

- **WHEN** a member is added to the `github.api.endpoint` enum and `make generate` is run
- **THEN** the generated list of `github.api.endpoint` values contains it

#### Scenario: No constants for imported metrics

- **WHEN** `make generate` is run
- **THEN** `internal/semconv/metrics.go` has no constant for any `go.*` or `http.*` metric

#### Scenario: No magic strings in collector code

- **WHEN** collector code records a metric or sets an attribute
- **THEN** it MUST reference a constant from `internal/semconv/`, not a string literal

### Requirement: Grafana dashboard generated from schema

The system SHALL generate a `dashboards/github-activity.json` Grafana dashboard from the Weaver registry using a Jinja2 template. Every panel query SHALL be built from the metric's `annotations.prometheus.name` and `annotations.prometheus.type` in UTF-8 PromQL syntax, with names and UTF-8 label names quoted; for an imported metric the annotation comes from its refinement. The dashboard SHALL define the variables `datasource`, `job`, `exporter.target`, `github.org`, `github.repo` and `min_step`.

#### Scenario: Dashboard regenerated on schema change

- **WHEN** a metric definition changes in the registry and `make generate` is run
- **THEN** the dashboard JSON is updated to reflect the new metric name or attribute

#### Scenario: Query shape follows the Prometheus type

- **WHEN** `make generate` renders a panel for a metric
- **THEN** a `counter` is queried through `rate` or `increase`, a `histogram` through `histogram_quantile` over `<name>_bucket`, a `gauge` through `last_over_time`, every window is `$__rate_interval`, and the metric name comes from `annotations.prometheus.name`

#### Scenario: Panel Min interval from a variable

- **WHEN** `make generate` renders the dashboard
- **THEN** every panel's Min interval is `$min_step`, a constant variable whose default is at least twice the default `export.interval` and twice the default `collection_interval`

#### Scenario: Continuous panels with a long collection interval

- **WHEN** the exporter runs with `collection_interval: 15m` and the default `export.interval`
- **THEN** every `rate()` panel has a value at every step of the dashboard's default Min interval, though that interval is shorter than `collection_interval`

#### Scenario: Panel references an unknown metric

- **WHEN** the template references a metric the registry does not define, or a panel needs a Prometheus type the metric does not have
- **THEN** `make generate` fails

#### Scenario: Dashboard contains panels for key metrics

- **WHEN** the dashboard is imported into Grafana
- **THEN** it contains panels for: PR created/merged/closed rates globally and per repo, open PR count, oldest open PR age, open PRs by review status, PR close duration p50/p90, automerged rate, open issue count, oldest open issue age, GitHub API rate limit per pool, scrape duration, API errors and OTLP push errors, a separate Dependency Dashboard row with the Renovate queue by section and parse errors, and a separate "Exporter internals" row with runtime memory by type, heap goal and limit, heap allocation rate, goroutine count, and GitHub API request latency p50/p90 per `url.template` and `http.response.status_code`

#### Scenario: Dependency Dashboard row without the collector

- **WHEN** no target enables the Renovate Dependency Dashboard collector
- **THEN** only the Dependency Dashboard row shows no data

#### Scenario: Exporter internals row without self-telemetry metrics

- **WHEN** self-telemetry metrics are inactive
- **THEN** only the Exporter internals row shows no data
