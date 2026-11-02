## MODIFIED Requirements

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
- **THEN** the attribute group `renovate_github_exporter.resource` declares `service.name` as required, plus the host, os, process and container attributes set by the SDK resource detectors in `internal/exporter/provider.go`

### Requirement: Live-check integration test

The system SHALL include an integration test that starts the exporter, collects metrics, and validates the emitted OTLP stream against the Weaver registry using `weaver registry live-check` in v2 mode. Live-check SHALL fail on any metric, metric attribute or resource attribute the registry does not declare, and on any missing required attribute.

#### Scenario: Emitted metrics conform to schema

- **WHEN** the integration test runs
- **THEN** `weaver registry live-check` exits with code 0 (no schema violations)

#### Scenario: Schema violation detected

- **WHEN** a metric is emitted with a wrong attribute type (e.g., int instead of string)
- **THEN** `weaver registry live-check` exits with a non-zero code

#### Scenario: Undeclared metric detected

- **WHEN** a metric not defined in the registry is emitted
- **THEN** `weaver registry live-check` exits with a non-zero code

#### Scenario: Undeclared attribute detected

- **WHEN** a registry metric is emitted with an attribute its `attributes:` block does not declare, even one declared on another metric
- **THEN** `weaver registry live-check` exits with a non-zero code
