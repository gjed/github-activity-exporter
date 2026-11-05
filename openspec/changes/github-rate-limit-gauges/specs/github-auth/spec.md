## MODIFIED Requirements

### Requirement: Rate limit awareness

The system SHALL monitor GitHub API rate limit consumption per target and pause requests when approaching the limit.

#### Scenario: Requests paused near rate limit

- **WHEN** fewer than 200 API requests remain in the current rate limit window for a target
- **THEN** the exporter pauses new API requests for that target until the window resets, and logs a warning

#### Scenario: Rate limit metrics exposed

- **WHEN** metrics are collected
- **THEN** `github_exporter.api.rate_limit.remaining` gauge and `github_exporter.api.rate_limit.reset` gauge are emitted per target (hooks into `observability-contract` self-metrics)

#### Scenario: Rate limit gauges follow the client state

- **WHEN** a GitHub API response for a target carries rate limit headers
- **THEN** `github_exporter.api.rate_limit.remaining` reports the remaining requests and `github_exporter.api.rate_limit.reset` reports the window reset as Unix time in seconds, both with `exporter.target` set to the target name
