## MODIFIED Requirements

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

### Requirement: Grafana dashboard generated from schema

The system SHALL generate a `dashboards/github-activity.json` Grafana dashboard JSON file from the Weaver registry using a Jinja2 template.

#### Scenario: Dashboard regenerated on schema change

- **WHEN** a metric definition changes in the registry and `make generate` is run
- **THEN** the dashboard JSON is updated to reflect the new metric name or attribute

#### Scenario: Dashboard contains panels for key metrics

- **WHEN** the dashboard is imported into Grafana
- **THEN** it contains panels for: open PR count by repo, PR age distribution, automerge rate, Renovate queue state (awaiting/rate-limited/pending)
