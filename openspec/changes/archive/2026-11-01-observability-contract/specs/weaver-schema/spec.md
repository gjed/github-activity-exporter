## ADDED Requirements

### Requirement: Weaver registry directory

The system SHALL contain a `registry/` directory at the repository root with a valid OTel Weaver semantic convention registry, including a `registry_manifest.yaml` and at least `attributes.yaml` and `signals.yaml`.

#### Scenario: Registry passes weaver check

- **WHEN** `weaver registry check -r registry/` is run
- **THEN** the command exits with code 0 and no errors

#### Scenario: Manifest declares OTel dependency

- **WHEN** the manifest is read
- **THEN** it declares a dependency on the official OTel semantic conventions (pinned version)

### Requirement: Metric schema covers all emitted metrics

The registry SHALL define every metric emitted by the exporter: name, instrument type (gauge/counter/histogram/updowncounter), unit, brief description, and all attributes with their types and requirement levels.

#### Scenario: All metric names present in registry

- **WHEN** the registry is resolved
- **THEN** it contains definitions for: `github.pr.count`, `github.pr.age`, `github.pr.close.duration`, `github.pr.automerged`, `github.issue.count`, `github.issue.age`, `renovate.dashboard.queue`, `github_exporter.scrape.duration`, `github_exporter.api.errors`, `github_exporter.otlp.errors`

#### Scenario: All attributes typed

- **WHEN** the registry is resolved
- **THEN** every attribute referenced by a metric signal has an explicit type (string, int, boolean) and at least one example value

### Requirement: Go constants generated from schema

The system SHALL generate a `internal/semconv/` Go package from the Weaver registry containing typed constants for all metric names and attribute key strings.

#### Scenario: Generated file is up to date

- **WHEN** `make generate` is run
- **THEN** `internal/semconv/metrics.go` and `internal/semconv/attributes.go` are regenerated and match the current registry

#### Scenario: No magic strings in collector code

- **WHEN** collector code records a metric or sets an attribute
- **THEN** it MUST reference a constant from `internal/semconv/`, not a string literal

### Requirement: Grafana dashboard generated from schema

The system SHALL generate a `dashboards/renovate-github.json` Grafana dashboard JSON file from the Weaver registry using a Jinja2 template.

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

The system SHALL include an integration test that starts the exporter, collects metrics, and validates the emitted OTLP stream against the Weaver registry using `weaver registry live-check`.

#### Scenario: Emitted metrics conform to schema

- **WHEN** the integration test runs
- **THEN** `weaver registry live-check` exits with code 0 (no schema violations)

#### Scenario: Schema violation detected

- **WHEN** a metric is emitted with a wrong attribute type (e.g., int instead of string)
- **THEN** `weaver registry live-check` exits with a non-zero code
