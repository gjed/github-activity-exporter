# Spec Delta

## MODIFIED Requirements

### Requirement: Incremental refresh

The system SHALL refresh the discovered repository list of each target every `discovery.refresh_interval` for as long as the target's collection loop runs, to pick up newly created repos and drop removed ones. A failed refresh SHALL keep the last successfully discovered list, be logged, and be counted in `github_exporter.api.errors`.

#### Scenario: New repo appears after refresh

- **WHEN** a new repository is created in a monitored org and one refresh interval elapses
- **THEN** the new repo is included in the next collection cycle

#### Scenario: Default refresh interval

- **WHEN** `discovery.refresh_interval` is not configured
- **THEN** the repo list is refreshed every 60 minutes

#### Scenario: Removed repo stops being collected

- **WHEN** a monitored repository is deleted, archived or filtered out and one refresh interval elapses
- **THEN** the next collection cycle no longer collects it and its series are no longer reported

#### Scenario: Failed refresh keeps the last list

- **WHEN** a refresh fails, for example on a GitHub 5xx
- **THEN** collection cycles keep using the last successfully discovered list, an error is logged naming the target, and `github_exporter.api.errors` is incremented with `github.api.endpoint` `rest.repos`

#### Scenario: Startup discovery runs once

- **WHEN** the exporter starts
- **THEN** each target lists its repositories once before its first collection cycle, not once for the cycle and again for the refresh
