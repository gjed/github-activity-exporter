## ADDED Requirements

### Requirement: Metric naming contract

The registry SHALL declare, in `registry/manifest.yaml`, that every exposed metric name equals the registry metric name exactly: no type or unit suffix (`_total`, `_seconds`, ...) and no underscore escaping. The only derived names SHALL be the classic histogram sample series `<name>_bucket`, `<name>_sum` and `<name>_count`. The contract SHALL state the backend requirements:

- Prometheus 3.x with UTF-8 metric names (`metric_name_validation_scheme: utf8`) and, for OTLP ingestion, the OTLP receiver enabled with `otlp.translation_strategy: NoTranslation`.
- Mimir with `name_validation_scheme: utf8` and `otel_translation_strategy: NoTranslation` and metric suffixes disabled (`-validation.name-validation-scheme=utf8`, `-distributor.otel-translation-strategy=NoTranslation`, `-distributor.otel-metric-suffixes-enabled=false`). Grafana Cloud Metrics needs the same per-tenant limits on the stack.

#### Scenario: Manifest states the contract

- **WHEN** `registry/manifest.yaml` is read
- **THEN** it states the identity naming rule, the histogram sample suffixes and the backend requirements above

#### Scenario: Prometheus name derivable from the registry

- **WHEN** a registry metric is named `github.pr.count`
- **THEN** its Prometheus-facing name is `github.pr.count`, and a registry histogram named `github_exporter.scrape.duration` is exposed as the family `github_exporter.scrape.duration` with samples `github_exporter.scrape.duration_bucket`, `_sum` and `_count`

## MODIFIED Requirements

### Requirement: Live-check integration test

The system SHALL include an integration test that starts the exporter, collects metrics, and validates the emitted OTLP stream against the Weaver registry using `weaver registry live-check` in v2 mode. Live-check SHALL fail on any metric, metric attribute or resource attribute the registry does not declare, and on any missing required attribute. The integration tests SHALL also emit every registry metric and fail unless the metric names in the live-check report are exactly the registry metric names.

#### Scenario: Emitted metrics conform to schema

- **WHEN** the integration test runs
- **THEN** `weaver registry live-check` exits with code 0 (no schema violations)

#### Scenario: OTLP names equal registry names

- **WHEN** the integration test emits every registry metric over OTLP
- **THEN** the metric names in the live-check report are exactly the registry metric names, with no suffix or escaping

#### Scenario: Schema violation detected

- **WHEN** a metric is emitted with a wrong attribute type (e.g., int instead of string)
- **THEN** `weaver registry live-check` exits with a non-zero code

#### Scenario: Undeclared metric detected

- **WHEN** a metric not defined in the registry is emitted
- **THEN** `weaver registry live-check` exits with a non-zero code

#### Scenario: Undeclared attribute detected

- **WHEN** a registry metric is emitted with an attribute its `attributes:` block does not declare, even one declared on another metric
- **THEN** `weaver registry live-check` exits with a non-zero code
