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

The registry SHALL define every metric emitted by the exporter: name, instrument type (gauge/counter/histogram/updowncounter), unit, brief description, signal `requirement_level`, and all attributes with their types and requirement levels. Every metric SHALL declare an `attributes:` block listing exactly the attributes the code attaches to it. The registry SHALL declare the resource attributes the exporter sets.

#### Scenario: All metric names present in registry

- **WHEN** the registry is resolved
- **THEN** it contains definitions for: `github.pr.count`, `github.pr.age`, `github.pr.close.duration`, `github.pr.automerged`, `github.issue.count`, `github.issue.age`, `renovate.dashboard.queue`, `github_exporter.scrape.duration`, `github_exporter.api.errors`, `github_exporter.otlp.errors`

#### Scenario: All attributes typed

- **WHEN** the registry is resolved
- **THEN** every attribute referenced by a metric signal has an explicit type (string, int, boolean) and at least one example value

#### Scenario: Every metric declares its attributes

- **WHEN** the registry is resolved
- **THEN** every metric has a `requirement_level` and an `attributes:` block, and every attribute reference has a requirement level

#### Scenario: API errors carry the endpoint

- **WHEN** the registry is resolved
- **THEN** `github_exporter.api.errors` declares `exporter.target` and `github.api.endpoint` as required attributes

#### Scenario: Resource attributes declared

- **WHEN** the registry is resolved
- **THEN** the attribute group `github_activity_exporter.resource` declares `service.name` as required, plus the host, os, process and container attributes set by the SDK resource detectors in `internal/exporter/provider.go`

#### Scenario: Resource identifies the exporter

- **WHEN** the exporter exports metrics
- **THEN** the resource carries `service.name` = `github-activity-exporter`

### Requirement: Go constants generated from schema

The system SHALL generate a `internal/semconv/` Go package from the Weaver registry containing typed constants for all metric names and attribute key strings.

#### Scenario: Generated file is up to date

- **WHEN** `make generate` is run
- **THEN** `internal/semconv/metrics.go` and `internal/semconv/attributes.go` are regenerated and match the current registry

#### Scenario: No magic strings in collector code

- **WHEN** collector code records a metric or sets an attribute
- **THEN** it MUST reference a constant from `internal/semconv/`, not a string literal

### Requirement: Grafana dashboard generated from schema

The system SHALL generate a `dashboards/github-activity.json` Grafana dashboard JSON file from the Weaver registry using a Jinja2 template.

#### Scenario: Dashboard regenerated on schema change

- **WHEN** a metric definition changes in the registry and `make generate` is run
- **THEN** the dashboard JSON is updated to reflect the new metric name or attribute

#### Scenario: Dashboard contains panels for key metrics

- **WHEN** the dashboard is imported into Grafana
- **THEN** it contains panels for: open PR count by repo, PR age distribution, automerge rate, Renovate queue state (awaiting/rate-limited/pending)

### Requirement: Schema validated in CI

The system SHALL run `weaver registry check` as a CI step on every pull request, failing the build if the registry is invalid.

#### Scenario: Invalid schema fails CI

- **WHEN** a PR introduces a registry YAML with a missing required field
- **THEN** the CI Weaver check job fails and blocks merge

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
