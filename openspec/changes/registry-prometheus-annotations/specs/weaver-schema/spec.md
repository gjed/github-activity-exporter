## MODIFIED Requirements

### Requirement: Schema validated in CI

The system SHALL run `weaver registry check --v2 -r registry/ -p policies/registry/` as a CI step on every pull request and from `make check-schema`, failing the build if the registry is invalid or violates a registry policy.

#### Scenario: Invalid schema fails CI

- **WHEN** a PR introduces a registry YAML with a missing required field
- **THEN** the CI Weaver check job fails and blocks merge

#### Scenario: Naming contract violation fails CI

- **WHEN** a registry metric lacks `annotations.prometheus.name` or `annotations.prometheus.type`, declares a `prometheus.name` different from the metric name, or declares a `prometheus.type` that does not follow from its instrument
- **THEN** `weaver registry check` exits non-zero with a `violation` naming the metric

### Requirement: Live-check integration test

The system SHALL include an integration test that starts the exporter, collects metrics, and validates the emitted OTLP stream against the Weaver registry using `weaver registry live-check` in v2 mode with the default advice policies. Live-check SHALL fail on any metric, metric attribute or resource attribute the registry does not declare, and on any missing required attribute. The integration tests SHALL also emit every registry metric and fail unless the metric names in the live-check report are exactly the registry metric names and the report has no violation other than a missing required attribute.

#### Scenario: Emitted metrics conform to schema

- **WHEN** the integration test runs
- **THEN** `weaver registry live-check` exits with code 0 (no schema violations)

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

Every registry metric SHALL declare its Prometheus exposure as `annotations.prometheus.name` and `annotations.prometheus.type`. `prometheus.name` SHALL equal the metric name exactly: no type or unit suffix (`_total`, `_seconds`, ...) and no underscore escaping. `prometheus.type` SHALL follow from the instrument: counter -> `counter`, updowncounter -> `gauge`, gauge -> `gauge`, histogram -> `histogram`. The only derived names SHALL be the classic histogram sample series `<name>_bucket`, `<name>_sum` and `<name>_count`. `registry/manifest.yaml` SHALL NOT restate the contract in prose; it SHALL point at the annotations. The backend requirements for these names SHALL be documented in `README.md`.

#### Scenario: Manifest states the contract

- **WHEN** `registry/manifest.yaml` is read
- **THEN** it points at the `annotations.prometheus` contract, the registry policy and the backend requirements in `README.md`, without restating the rule

#### Scenario: Every metric declares the annotation

- **WHEN** the registry is resolved
- **THEN** every metric has `annotations.prometheus.name` equal to its name and `annotations.prometheus.type` derived from its instrument

#### Scenario: Prometheus name derivable from the registry

- **WHEN** a registry metric is named `github.pr.count`
- **THEN** its Prometheus-facing name is `github.pr.count`, and a registry histogram named `github_exporter.scrape.duration` is exposed as the family `github_exporter.scrape.duration` with samples `github_exporter.scrape.duration_bucket`, `_sum` and `_count`

## ADDED Requirements

### Requirement: Prometheus exposition verified by live-check

The integration tests SHALL scrape `/metrics`, convert the exposition to live-check samples and run `weaver registry live-check` on them with a policy that fails when a family's exposed type differs from `annotations.prometheus.type`. The run SHALL use its own weaver configuration that downgrades the built-in `unexpected_instrument` and `unit_mismatch` findings, since the exposition carries no unit and exposes updowncounters as `gauge`.

#### Scenario: Exposition conforms to the contract

- **WHEN** every registry metric is recorded and `/metrics` is scraped with `Accept: application/openmetrics-text;version=1.0.0` first, or with plain text
- **THEN** live-check sees exactly the registry metric names, no non-registry metric, and no violation other than a missing required attribute

#### Scenario: Suffixed name detected

- **WHEN** the exposition exposes `github.pr.automerged` as `github.pr.automerged_total`
- **THEN** live-check exits non-zero and reports the metric as not in the registry

#### Scenario: Wrong exposed type detected

- **WHEN** the exposition types `github.pr.automerged` as `gauge`
- **THEN** live-check exits non-zero with a type-mismatch violation naming `annotations.prometheus.type`
