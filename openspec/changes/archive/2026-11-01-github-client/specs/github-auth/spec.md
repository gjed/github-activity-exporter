## ADDED Requirements

### Requirement: PAT authentication

The system SHALL support GitHub Personal Access Token authentication, configurable per target via config file or environment variable.

#### Scenario: Token from config file

- **WHEN** a target has `auth.token` set in the config file
- **THEN** all API requests for that target use `Authorization: Bearer <token>`

#### Scenario: Token from environment variable

- **WHEN** a target has `auth.token_env: MY_GITHUB_TOKEN` configured
- **THEN** the token is read from that environment variable at startup; missing env var is a fatal error

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

The system SHALL monitor GitHub API rate limit consumption per target and pause requests when approaching the limit.

#### Scenario: Requests paused near rate limit

- **WHEN** fewer than 200 API requests remain in the current rate limit window for a target
- **THEN** the exporter pauses new API requests for that target until the window resets, and logs a warning

#### Scenario: Rate limit metrics exposed

- **WHEN** metrics are collected
- **THEN** `github_exporter.api.rate_limit.remaining` gauge and `github_exporter.api.rate_limit.reset` gauge are emitted per target (hooks into `observability-contract` self-metrics)
