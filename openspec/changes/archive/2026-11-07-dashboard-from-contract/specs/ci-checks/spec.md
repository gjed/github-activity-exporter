## ADDED Requirements

### Requirement: Dashboard queries verified end to end on every PR

The system SHALL run an end-to-end check on every pull request that feeds the exporter binary, run against a mock GitHub API with the Dependency Dashboard collector on, into Prometheus 3.x over OTLP (UTF-8 names, `NoTranslation`), then runs every query in the generated dashboard with variables substituted. It SHALL fail when any query errors or returns an empty result.

#### Scenario: Every panel query returns data

- **WHEN** the e2e check runs against the generated dashboard
- **THEN** every panel target and every variable query returns a non-empty result without error, and the job passes

#### Scenario: Broken query fails CI

- **WHEN** a dashboard query uses a name, suffix or shape the backend does not have (for example an unquoted dotted name or a histogram without `_bucket`)
- **THEN** the e2e check fails and names the panel, the query and the Prometheus response

#### Scenario: Make target

- **WHEN** a developer runs `make test-e2e` with Docker available
- **THEN** the same check runs locally
