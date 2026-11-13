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

### Requirement: Secondary rate limit pause

The system SHALL treat a 403 or 429 response as a secondary rate limit when it carries `Retry-After`, or when it is a 429 or its body names a secondary rate limit and `X-RateLimit-Remaining` is not 0. It SHALL then pause every request of the target, in every pool, until the time GitHub gives, and log a warning with the wait.

#### Scenario: Retry-After

- **WHEN** a request of a target gets a 403 with `Retry-After: 30`
- **THEN** no request of that target is sent for 30 seconds, whichever pool it counts against

#### Scenario: No wait given

- **WHEN** a request gets a 429 with neither `Retry-After` nor `X-RateLimit-Remaining: 0`
- **THEN** the target pauses for 60 seconds, doubling on each further consecutive secondary rate limit response of the same request

#### Scenario: Other targets unaffected

- **WHEN** one target is paused for a secondary rate limit
- **THEN** requests of other targets are sent without waiting

#### Scenario: Not a rate limit

- **WHEN** a request gets a 403 without `Retry-After`, with `X-RateLimit-Remaining` above 0, and a body that names no rate limit
- **THEN** the response is returned as is, with no pause and no retry

### Requirement: Rate limit retry

After a secondary rate limit pause, or a 403 or 429 with `X-RateLimit-Remaining: 0` once its pool's window resets, the system SHALL send the request again, at most 3 times. When the retries are spent the last response SHALL be returned to the caller.

#### Scenario: Retry succeeds

- **WHEN** a request gets a secondary rate limit response and the retry after the pause gets 200
- **THEN** the caller receives the 200 response and the repo is collected normally

#### Scenario: Retries spent

- **WHEN** a request gets a secondary rate limit response on the first attempt and on 3 retries
- **THEN** the caller receives the last 403 or 429 response, and the repo's collection fails as with any other API error

#### Scenario: Primary limit exhausted

- **WHEN** a request to the `graphql` pool gets a 403 with `X-RateLimit-Remaining: 0` and `X-RateLimit-Reset` in 10 minutes
- **THEN** only the `graphql` pool is paused until the reset, the request is sent again after it, and requests to `core` are sent without waiting

#### Scenario: Shutdown during a pause

- **WHEN** the request's context is cancelled during a pause
- **THEN** the request returns the context error at once

### Requirement: Rate limit and auth handling with concurrent requests

The primary pool pause, the secondary rate limit pause and the 401 reporting used by `/healthz` SHALL behave the same when many requests of a target run at once.

#### Scenario: Concurrent requests during a pool pause

- **WHEN** a pool of a target is paused and several workers send requests to it
- **THEN** every one of them waits for the reset, and the race detector reports no data race

#### Scenario: Concurrent requests during a secondary pause

- **WHEN** one worker's request gets a secondary rate limit response while other workers of the target are sending requests
- **THEN** every request of the target sent after that response waits for the same pause

#### Scenario: 401 from concurrent requests

- **WHEN** concurrent requests of a target are answered with 401
- **THEN** each response is reported once to the health tracker, as with serial requests

### Requirement: Required GitHub permissions

A target's credential SHALL need only the permissions of the calls its discovery mode and enabled collectors make. GitHub App and fine-grained PAT: Metadata, Pull requests and Issues, read. Classic PAT: `repo` scope. `GET /users/{owner}`, `GET /user` and public repos need no permission. The Dependency Dashboard collector needs Issues read, already required by the issue collector.

#### Scenario: GitHub App with the minimum permissions

- **WHEN** a GitHub App installation grants Metadata, Pull requests and Issues read-only and nothing else
- **THEN** discovery and every collector of the target succeed

#### Scenario: Fine-grained PAT with the minimum permissions

- **WHEN** a fine-grained PAT grants Metadata, Pull requests and Issues read-only on the monitored repos
- **THEN** discovery and every collector of the target succeed

#### Scenario: Classic PAT with repo scope

- **WHEN** a classic PAT has the `repo` scope and no other
- **THEN** discovery and every collector of the target succeed, private repos included

#### Scenario: Write access satisfies read

- **WHEN** a credential grants write access for a permission the exporter needs read access for
- **THEN** that permission counts as granted

### Requirement: Permission check on startup

On startup the system SHALL check each target's granted permissions against the required ones, after the credential check and before the target's first collection cycle, and exit with one error naming the target and every missing permission.

#### Scenario: GitHub App missing a permission

- **WHEN** a GitHub App installation does not grant Issues read
- **THEN** the exporter exits at startup with an error naming the target and `issues: read`

#### Scenario: GitHub App repository selection

- **WHEN** a GitHub App installation has repository selection `selected`
- **THEN** the exporter logs the target and the selection, and discovers only the selected repos

#### Scenario: Classic PAT missing the repo scope

- **WHEN** GitHub answers a target's token with an `X-OAuth-Scopes` header that does not contain `repo`
- **THEN** the exporter exits at startup with an error naming the target and the `repo` scope

#### Scenario: Fine-grained PAT missing a permission

- **WHEN** a target's token gets no `X-OAuth-Scopes` header and reading pull requests of a private repo the token can see is forbidden
- **THEN** the exporter exits at startup with an error naming the target and `pull_requests: read`

#### Scenario: Fine-grained PAT with public repos only

- **WHEN** a fine-grained PAT can see no private repo of a configured owner
- **THEN** the check passes for that owner, since every token can read public repos

#### Scenario: Several missing permissions

- **WHEN** a target's credential lacks both Pull requests and Issues read
- **THEN** one error names the target and both permissions

#### Scenario: Not ready before the check

- **WHEN** a target's permission check has not passed
- **THEN** `/readyz` reports the target's credential check as pending, and a failed check exits the exporter before any target becomes ready

### Requirement: Permission probes and rate limits

Permission probes SHALL use the target's GitHub client: they wait on a paused rate-limit pool and on a secondary rate limit pause like any request, are not counted against the target's collection concurrency, and record rate-limit state only for pools of the target's own credential.

#### Scenario: Probe during a secondary rate limit pause

- **WHEN** the target is paused after a secondary rate limit when the permission check starts
- **THEN** the probe is sent after the pause ends

#### Scenario: App installation permissions need no request

- **WHEN** the target authenticates as a GitHub App
- **THEN** the permissions are read from the installation token exchange the exporter already makes, and no request is sent with the App's JWT
