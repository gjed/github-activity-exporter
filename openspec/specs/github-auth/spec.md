# github-auth Specification

## Purpose

Authenticate to the GitHub API with a PAT or a GitHub App installation token, refresh tokens automatically, and pause requests before the rate limit is exhausted.

## Requirements

### Requirement: PAT authentication

The system SHALL support GitHub Personal Access Token authentication, configurable per target via config file or environment variable. A target with no `auth` settings SHALL use the `GITHUB_TOKEN` environment variable as its PAT.

#### Scenario: Token from config file

- **WHEN** a target has `auth.token` set in the config file
- **THEN** all API requests for that target use `Authorization: Bearer <token>`

#### Scenario: Token from environment variable

- **WHEN** a target has `auth.token_env: MY_GITHUB_TOKEN` configured
- **THEN** the token is read from that environment variable at startup; missing env var is a fatal error

#### Scenario: Empty auth falls back to GITHUB_TOKEN

- **WHEN** a target's `auth` block is empty or omitted and `GITHUB_TOKEN` is set to a non-empty value
- **THEN** all API requests for that target use `GITHUB_TOKEN` as a PAT

#### Scenario: No auth and no GITHUB_TOKEN is fatal

- **WHEN** a target's `auth` block is empty or omitted and `GITHUB_TOKEN` is unset or empty
- **THEN** the exporter exits at startup with an error that names the target and lists `auth.token`, `auth.token_env`, `auth.app` and `GITHUB_TOKEN` as ways to supply a credential

#### Scenario: Explicit auth takes precedence over GITHUB_TOKEN

- **WHEN** a target sets `auth.token`, `auth.token_env` or `auth.app` and `GITHUB_TOKEN` is also set
- **THEN** the target uses its explicit auth and `GITHUB_TOKEN` is ignored for that target

#### Scenario: Partial auth block does not fall back

- **WHEN** a target's `auth` block sets some fields but no complete method (e.g. `auth.app.installation_id` without `auth.app.app_id`)
- **THEN** the exporter exits at startup with an auth validation error naming the target, and does not use `GITHUB_TOKEN`

### Requirement: GitHub App authentication

The system SHALL support GitHub App authentication using JWT signing and installation token exchange.

#### Scenario: Installation token obtained on startup

- **WHEN** a target is configured with `auth.app.app_id`, `auth.app.installation_id`, and `auth.app.private_key_path`
- **THEN** the exporter generates a signed JWT and exchanges it for an installation token before making any API calls

#### Scenario: Private key from environment variable

- **WHEN** `auth.app.private_key_env` is set (base64-encoded PEM)
- **THEN** the private key is decoded from that environment variable instead of read from disk

#### Scenario: Installation token refreshed before expiry

- **WHEN** the cached installation token has fewer than 5 minutes remaining before its 1-hour expiry
- **THEN** the exporter automatically refreshes the token without requiring a restart or returning an error to callers

### Requirement: Credential isolation per target

The system SHALL ensure that credentials configured for one target are never used for API calls belonging to another target.

#### Scenario: Two targets use different credentials

- **WHEN** target A uses a GitHub App and target B uses a PAT
- **THEN** no API call for target A uses target B's credentials, and vice versa

### Requirement: Credential validation on startup

The system SHALL verify credentials by making a lightweight API call (e.g., `GET /user` for PAT, `GET /app` for GitHub App) on startup and fail with a clear error message if authentication fails.

#### Scenario: Invalid PAT fails fast

- **WHEN** the configured PAT is invalid or expired
- **THEN** the exporter exits at startup with an error message identifying the target and the auth failure reason

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

### Requirement: Per-target API base URL

The system SHALL let each target set `base_url`, the GitHub REST API root, defaulting to `https://api.github.com`. Every outbound GitHub call for that target, including REST, GraphQL, GitHub App token exchange and the startup credential check, SHALL go to the host and path derived from that target's `base_url`.

#### Scenario: Default targets github.com

- **WHEN** a target does not set `base_url`
- **THEN** REST calls go to `https://api.github.com` and GraphQL calls go to `https://api.github.com/graphql`

#### Scenario: GitHub Enterprise Server

- **WHEN** a target sets `base_url: https://ghes.example.com/api/v3`
- **THEN** REST calls, the GitHub App installation token exchange and the startup credential check go to `https://ghes.example.com/api/v3`, GraphQL calls go to `https://ghes.example.com/api/graphql`, and no call for that target goes to `api.github.com`

#### Scenario: Two targets with different base URLs stay isolated

- **WHEN** target A uses one `base_url` and target B uses another
- **THEN** no API call for target A goes to target B's host, and vice versa

#### Scenario: Invalid base URL is fatal

- **WHEN** a target's `base_url` is not an `http` or `https` URL with a host, or carries a query string or fragment
- **THEN** the exporter exits at startup with an error naming the target and the `base_url` value
