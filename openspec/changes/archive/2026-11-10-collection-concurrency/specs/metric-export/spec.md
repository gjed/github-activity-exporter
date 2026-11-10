# Spec Delta

## ADDED Requirements

### Requirement: Secondary rate limit counter

The system SHALL count every GitHub response it treats as a secondary rate limit in `github_exporter.api.secondary_rate_limits`, a counter with the `exporter.target` attribute, and SHALL export it with value 0 for every target from the first push.

#### Scenario: Counted per response

- **WHEN** a request of target `zextras` gets a secondary rate limit response and its retry gets another one
- **THEN** `github_exporter.api.secondary_rate_limits{exporter.target="zextras"}` increases by 2

#### Scenario: Exported before the first hit

- **WHEN** a target's collection loop starts
- **THEN** `github_exporter.api.secondary_rate_limits` is exported with value 0 for that target

#### Scenario: Primary exhaustion not counted

- **WHEN** a request gets a 403 with `X-RateLimit-Remaining: 0` and no `Retry-After`
- **THEN** `github_exporter.api.secondary_rate_limits` does not change
