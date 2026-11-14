# weaver-schema Specification

## Purpose

Define every metric and attribute the exporter emits in an OTel Weaver registry, validate it in CI, and generate Go constants and the Grafana dashboard from it.

## Requirements

### Requirement: Weaver registry directory

The system SHALL contain a `registry/` directory at the repository root with a valid OTel Weaver semantic convention registry, including a `manifest.yaml` (the weaver default manifest name, with `file_format: definition_manifest/2.0`) and at least `attributes.yaml` and `signals.yaml`.

#### Scenario: Registry passes weaver check

- **WHEN** `weaver registry check -r registry/` is run
- **THEN** the command exits with code 0 and reports no errors and no missing `requirement_level` warnings

#### Scenario: Manifest declares OTel dependency

- **WHEN** the manifest is read
- **THEN** it declares a dependency on the official OTel semantic conventions (pinned version)

### Requirement: Metric schema covers all emitted metrics

The registry SHALL define every metric emitted by the exporter: name, instrument type (gauge/counter/histogram/updowncounter), unit, brief description, signal `requirement_level`, and all attributes with their types and requirement levels. Every metric SHALL declare an `attributes:` block listing exactly the attributes the code attaches to it. The registry SHALL declare the resource attributes the exporter sets. A metric defined by the OTel semantic conventions that the registry depends on SHALL be declared by importing it from that dependency and refining it, not by redefining it; the refinement SHALL remove any attribute the exporter never attaches.

#### Scenario: All metric names present in registry

- **WHEN** the registry is resolved
- **THEN** it contains definitions for: `github.pr.count`, `github.pr.age`, `github.pr.close.duration`, `github.pr.automerged`, `github.pr.created`, `github.pr.merged`, `github.pr.closed`, `github.issue.count`, `github.issue.age`, `renovate.dashboard.queue`, `github_exporter.scrape.duration`, `github_exporter.api.errors`, `github_exporter.otlp.errors`, `github_exporter.api.rate_limit.remaining`, `github_exporter.api.rate_limit.reset`

#### Scenario: Self-telemetry metrics imported from OTel semantic conventions

- **WHEN** the registry is resolved
- **THEN** `go.memory.used`, `go.memory.limit`, `go.memory.allocated`, `go.memory.allocations`, `go.memory.gc.goal`, `go.goroutine.count`, `go.processor.limit`, `go.config.gogc`, `go.schedule.duration`, `http.client.request.duration` and `http.client.request.body.size` come from the pinned OTel semantic conventions dependency through `imports`, and no file in `registry/` redefines them or their attributes

#### Scenario: All attributes typed

- **WHEN** the registry is resolved
- **THEN** every attribute referenced by a metric signal has an explicit type (string, int, boolean) and at least one example value

#### Scenario: Every metric declares its attributes

- **WHEN** the registry is resolved
- **THEN** every metric has a `requirement_level` and an `attributes:` block, and every attribute reference has a requirement level

#### Scenario: API errors carry the endpoint

- **WHEN** the registry is resolved
- **THEN** `github_exporter.api.errors` declares `exporter.target` and `github.api.endpoint` as required attributes

#### Scenario: Rate limit gauges carry the target

- **WHEN** the registry is resolved
- **THEN** `github_exporter.api.rate_limit.remaining` and `github_exporter.api.rate_limit.reset` are gauges that declare `exporter.target` and `github.api.rate_limit.resource` as required attributes, and `github.api.rate_limit.resource` is an enum closed to the pools GitHub documents (`core`, `search`, `code_search`, `graphql`, `integration_manifest`, `source_import`, `actions_runner_registration`, `scim`, `dependency_snapshots`, `dependency_sbom`, `code_scanning_upload`, `code_scanning_autofix`, `audit_log`) plus `other`

#### Scenario: Resource attributes declared

- **WHEN** the registry is resolved
- **THEN** the attribute group `github_activity_exporter.resource` declares `service.name`, `service.version` and `service.instance.id` as required, plus the host, os, process and container attributes set by the SDK resource detectors

#### Scenario: Resource identifies the exporter

- **WHEN** the exporter exports metrics with no `OTEL_SERVICE_NAME` or `service.name` in `OTEL_RESOURCE_ATTRIBUTES`
- **THEN** the resource carries `service.name` = `github-activity-exporter`

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

### Requirement: Schema validated in CI

The system SHALL run `weaver registry check --v2 -r registry/ -p policies/registry/` as a CI step on every pull request and from `make check-schema`, failing the build if the registry is invalid or violates a registry policy.

#### Scenario: Invalid schema fails CI

- **WHEN** a PR introduces a registry YAML with a missing required field
- **THEN** the CI Weaver check job fails and blocks merge

#### Scenario: Naming contract violation fails CI

- **WHEN** a registry metric lacks `annotations.prometheus.name` or `annotations.prometheus.type`, declares a `prometheus.name` different from the metric name, or declares a `prometheus.type` that does not follow from its instrument
- **THEN** `weaver registry check` exits non-zero with a `violation` naming the metric

### Requirement: Live-check integration test

The system SHALL include an integration test that starts the exporter, collects metrics, and validates the emitted OTLP stream against the Weaver registry using `weaver registry live-check` in v2 mode with the default advice policies. Live-check SHALL fail on any metric, metric attribute or resource attribute the registry does not declare, and on any missing required attribute. The integration tests SHALL also emit every registry metric and fail unless the metric names in the live-check report are exactly the registry metric names and the report has no violation other than a missing required attribute. The OTLP run SHALL be made with self-telemetry metrics active, sending GitHub requests through the instrumented client, and with them inactive.

#### Scenario: Emitted metrics conform to schema

- **WHEN** the integration test runs
- **THEN** `weaver registry live-check` exits with code 0 (no schema violations)

#### Scenario: Self-telemetry metrics conform to schema

- **WHEN** the integration test runs with self-telemetry metrics active and GitHub requests sent through the instrumented client
- **THEN** live-check sees the `go.*` and `http.client.*` metrics, exits with code 0, and reports no undeclared metric or attribute

#### Scenario: No self-telemetry metrics when inactive

- **WHEN** the integration test runs with self-telemetry metrics inactive
- **THEN** live-check sees no `go.*` and no `http.*` metric

#### Scenario: OTLP names equal registry names

- **WHEN** the integration test emits every registry metric over OTLP without attributes
- **THEN** the metric names in the live-check report are exactly the registry metric names, with no suffix or escaping, and every violation in the report is `required_attribute_not_present`

#### Scenario: Schema violation detected

- **WHEN** a metric is emitted with a wrong attribute type (e.g., int instead of string)
- **THEN** `weaver registry live-check` exits with a non-zero code

#### Scenario: Undeclared metric detected

- **WHEN** a metric not defined in the registry is emitted
- **THEN** `weaver registry live-check` exits with a non-zero code

#### Scenario: Undeclared attribute detected

- **WHEN** a registry metric is emitted with an attribute its `attributes:` block does not declare, even one declared on another metric
- **THEN** `weaver registry live-check` exits with a non-zero code

### Requirement: Metric naming contract

Every registry metric SHALL declare its Prometheus exposure as `annotations.prometheus.name` and `annotations.prometheus.type`; a metric imported from the OTel semantic conventions dependency SHALL declare them on its refinement. `prometheus.name` SHALL equal the metric name exactly: no type or unit suffix (`_total`, `_seconds`, ...) and no underscore escaping. `prometheus.type` SHALL follow from the instrument: counter -> `counter`, updowncounter -> `gauge`, gauge -> `gauge`, histogram -> `histogram`. The only derived names SHALL be the classic histogram sample series `<name>_bucket`, `<name>_sum` and `<name>_count`. `registry/manifest.yaml` SHALL NOT restate the contract in prose; it SHALL point at the annotations. The backend requirements for these names SHALL be documented in `README.md`.

#### Scenario: Manifest states the contract

- **WHEN** `registry/manifest.yaml` is read
- **THEN** it points at the `annotations.prometheus` contract, the registry policy and the backend requirements in `README.md`, without restating the rule

#### Scenario: Every metric declares the annotation

- **WHEN** the registry is resolved
- **THEN** every metric has `annotations.prometheus.name` equal to its name and `annotations.prometheus.type` derived from its instrument, on its own definition or, for an imported metric, on its refinement

#### Scenario: Imported metric without the annotation

- **WHEN** a metric is imported from the OTel semantic conventions dependency and no refinement declares its `annotations.prometheus`
- **THEN** `weaver registry check` exits non-zero with a `violation` naming the metric

#### Scenario: Prometheus name derivable from the registry

- **WHEN** a registry metric is named `github.pr.count`
- **THEN** its Prometheus-facing name is `github.pr.count`, and a registry histogram named `github_exporter.scrape.duration` is exposed as the family `github_exporter.scrape.duration` with samples `github_exporter.scrape.duration_bucket`, `_sum` and `_count`

### Requirement: Prometheus exposition verified by live-check

The integration tests SHALL scrape `/metrics`, convert the exposition to live-check samples and run `weaver registry live-check` on them with a policy that fails when a family's exposed type differs from `annotations.prometheus.type`, including for imported metrics. The run SHALL use its own weaver configuration that downgrades the built-in `unexpected_instrument`, `unit_mismatch` and attribute `type_mismatch` findings, since the exposition carries no unit, exposes updowncounters as `gauge` and has only string label values. The `/metrics` run SHALL be made with self-telemetry metrics active and inactive.

#### Scenario: Exposition conforms to the contract

- **WHEN** every registry metric is recorded and `/metrics` is scraped with `Accept: application/openmetrics-text;version=1.0.0` first, or with plain text
- **THEN** live-check sees exactly the registry metric names, no non-registry metric, and no violation other than a missing required attribute

#### Scenario: Self-telemetry metrics on /metrics

- **WHEN** self-telemetry metrics are active and `/metrics` is scraped
- **THEN** live-check sees the `go.*` and `http.client.*` families, each with the type its `annotations.prometheus.type` declares, and no violation; with self-telemetry metrics inactive no `go.*` or `http.*` family is exposed

#### Scenario: Suffixed name detected

- **WHEN** the exposition exposes `github.pr.automerged` as `github.pr.automerged_total`
- **THEN** live-check exits non-zero and reports the metric as not in the registry

#### Scenario: Wrong exposed type detected

- **WHEN** the exposition types `github.pr.automerged` as `gauge`
- **THEN** live-check exits non-zero with a type-mismatch violation naming `annotations.prometheus.type`

### Requirement: Span schema

The registry SHALL declare every span the exporter creates: its own spans as `definition/2` span groups with their attributes, and GitHub HTTP client spans by reference to the semconv `http.client` span of the pinned dependency, with the GraphQL operation attributes referenced from the dependency.

#### Scenario: Registry check

- **WHEN** `make check-schema` runs
- **THEN** it validates the span groups with the metrics

### Requirement: Span live-check

The live-check integration tests SHALL export the spans of collection cycles to `weaver registry live-check`, pair each span with its span group through `.weaver.toml` matchers, and fail on any violation or on any span sample that matches no span group.

#### Scenario: Undeclared span

- **WHEN** the exporter emits a span whose name matches no matcher
- **THEN** the span live-check test fails
