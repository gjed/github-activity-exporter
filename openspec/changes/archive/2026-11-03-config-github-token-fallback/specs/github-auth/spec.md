# Spec Delta

## MODIFIED Requirements

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
