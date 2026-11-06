## MODIFIED Requirements

### Requirement: Metric schema covers all emitted metrics

The registry SHALL define every metric emitted by the exporter: name, instrument type (gauge/counter/histogram/updowncounter), unit, brief description, signal `requirement_level`, and all attributes with their types and requirement levels. Every metric SHALL declare an `attributes:` block listing exactly the attributes the code attaches to it. The registry SHALL declare the resource attributes the exporter sets.

#### Scenario: All metric names present in registry

- **WHEN** the registry is resolved
- **THEN** it contains definitions for: `github.pr.count`, `github.pr.age`, `github.pr.close.duration`, `github.pr.automerged`, `github.issue.count`, `github.issue.age`, `renovate.dashboard.queue`, `github_exporter.scrape.duration`, `github_exporter.api.errors`, `github_exporter.otlp.errors`, `github_exporter.api.rate_limit.remaining`, `github_exporter.api.rate_limit.reset`

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
- **THEN** the attribute group `github_activity_exporter.resource` declares `service.name` as required, plus the host, os, process and container attributes set by the SDK resource detectors in `internal/exporter/provider.go`

#### Scenario: Resource identifies the exporter

- **WHEN** the exporter exports metrics
- **THEN** the resource carries `service.name` = `github-activity-exporter`
