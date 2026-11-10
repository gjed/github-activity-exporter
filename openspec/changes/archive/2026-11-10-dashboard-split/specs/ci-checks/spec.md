## MODIFIED Requirements

### Requirement: Helm chart check on chart PRs

The system SHALL run `helm lint`, `helm template` and `kubeconform -strict` on the chart, with default values and with each example values file, on every pull request that changes `charts/`, a generated `dashboards/*.json` file or the chart workflow. The check SHALL fail on any lint error, render error or schema violation.

#### Scenario: Invalid manifest blocks merge

- **WHEN** a PR changes a chart template so that a rendered manifest violates the Kubernetes schema
- **THEN** the chart check fails

#### Scenario: Dashboard change runs the check

- **WHEN** a PR changes only `dashboards/github-activity-exporter.json`
- **THEN** the chart check runs

#### Scenario: Unrelated PR skips the check

- **WHEN** a PR changes only Go code
- **THEN** the chart check does not run

### Requirement: Dashboard queries verified end to end on every PR

The system SHALL run an end-to-end check on every pull request that feeds the exporter binary, run against a mock GitHub API with the Dependency Dashboard collector on, into Prometheus 3.x over OTLP (UTF-8 names, `NoTranslation`), then runs every query of every generated dashboard with variables substituted. It SHALL run once with self-telemetry metrics on and once with them off. It SHALL fail when any query errors, or returns an empty result unless the query reads a self-telemetry metric and self-telemetry metrics are off, in which case it SHALL fail on a non-empty result.

#### Scenario: Every panel query returns data

- **WHEN** the e2e check runs with self-telemetry metrics on
- **THEN** every panel target and every variable query of every generated dashboard returns a non-empty result without error

#### Scenario: KPI dashboard complete without self-telemetry

- **WHEN** the e2e check runs with self-telemetry metrics off
- **THEN** every KPI dashboard target and variable returns a non-empty result, every exporter dashboard target outside the self-telemetry row returns a non-empty result, and every self-telemetry target returns an empty one

#### Scenario: Broken query fails CI

- **WHEN** a dashboard query uses a name, suffix or shape the backend does not have (for example an unquoted dotted name or a histogram without `_bucket`)
- **THEN** the e2e check fails and names the dashboard, the panel, the query and the Prometheus response

#### Scenario: Make target

- **WHEN** a developer runs `make test-e2e` with Docker available
- **THEN** the same check runs locally
