## MODIFIED Requirements

### Requirement: Rate limit awareness

The system SHALL monitor GitHub API rate limit consumption per target and per rate limit pool (`X-RateLimit-Resource`: `core`, `graphql`, `search`, ...) and pause a request only when the pool it counts against is approaching its limit.

#### Scenario: Requests paused near rate limit

- **WHEN** fewer than 200 API requests remain in the current rate limit window of a pool for a target
- **THEN** the exporter pauses new API requests for that target that count against that pool until the window resets, and logs a warning naming the pool

#### Scenario: Low pool does not pause other pools

- **WHEN** one pool for a target is paused (for example `graphql`) and a request for that target counts against another pool (for example `core`)
- **THEN** the request is sent without waiting

#### Scenario: Pool determined before the request

- **WHEN** a request is about to be sent
- **THEN** its pool is `graphql` for the GraphQL endpoint, `code_search` for REST code search, `search` for other REST search endpoints, and `core` for every other REST endpoint

#### Scenario: Response pool from the header

- **WHEN** a GitHub API response carries `X-RateLimit-Remaining`
- **THEN** the rate limit state is recorded under the pool named by `X-RateLimit-Resource`; under `other` if that value is not a documented pool; and under the pool determined before the request if the header is missing

#### Scenario: Rate limit metrics exposed

- **WHEN** metrics are collected
- **THEN** `github_exporter.api.rate_limit.remaining` gauge and `github_exporter.api.rate_limit.reset` gauge are emitted per target and per pool (hooks into `observability-contract` self-metrics)

#### Scenario: Rate limit gauges follow the client state

- **WHEN** a GitHub API response for a target carries rate limit headers
- **THEN** `github_exporter.api.rate_limit.remaining` reports the remaining requests and `github_exporter.api.rate_limit.reset` reports the window reset as Unix time in seconds, both with `exporter.target` set to the target name and `github.api.rate_limit.resource` set to the pool the state was recorded under

#### Scenario: Gauges keep one series per pool

- **WHEN** responses for a target alternate between pools
- **THEN** each pool keeps its own series with its own last value, and an update to one pool does not change the series of another
