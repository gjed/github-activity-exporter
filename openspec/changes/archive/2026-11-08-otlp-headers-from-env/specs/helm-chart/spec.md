## MODIFIED Requirements

### Requirement: Secret exposure as env and files

Each entry of `secret.env` SHALL become a container env var read from the given Secret key. Each key listed in `secret.files` SHALL be mounted read-only at `/etc/github-activity-exporter/secrets/<key>`, readable by the container user and no other user.

#### Scenario: OTLP headers from the Secret

- **WHEN** `secret.env` maps `OTLP_HEADERS` to a key holding `authorization=Bearer x` and the config sets `export.otlp.headers_env: OTLP_HEADERS`
- **THEN** the exporter sends `authorization: Bearer x` on product OTLP requests

#### Scenario: GitHub App key as a file

- **WHEN** `secret.files` lists `app-key.pem` and the config sets `auth.app.private_key_path: /etc/github-activity-exporter/secrets/app-key.pem`
- **THEN** the exporter reads the private key from that file

## ADDED Requirements

### Requirement: Secret env before extra env

The container env list SHALL hold every `secret.env` var before any `env` entry, so an `env` value can reference a Secret-backed var with Kubernetes `$(VAR)` expansion.

#### Scenario: Bearer header from a raw token

- **WHEN** `secret.env` maps `OTLP_TOKEN` to a key holding the raw token, `env` holds `OTLP_HEADERS` with value `authorization=Bearer $(OTLP_TOKEN)`, and the config sets `export.otlp.headers_env: OTLP_HEADERS`
- **THEN** `OTLP_TOKEN` precedes `OTLP_HEADERS` in the rendered container env and the exporter sends `authorization: Bearer <token>`
