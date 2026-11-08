## ADDED Requirements

### Requirement: OTLP headers from an environment variable

When `export.otlp.headers_env` names an env var, the system SHALL read OTLP headers from it as comma-separated `name=value` pairs in the `OTEL_EXPORTER_OTLP_HEADERS` format and merge them into `export.otlp.headers`, the env var winning for the same header name. Header names SHALL be compared case-insensitively. The merged headers SHALL apply wherever `export.otlp.headers` apply.

#### Scenario: Auth header from the environment

- **WHEN** `export.otlp.headers_env: OTLP_HEADERS` is configured and `OTLP_HEADERS=authorization=Bearer s3cret`
- **THEN** every OTLP push request carries `authorization: Bearer s3cret`

#### Scenario: Merged with file headers

- **WHEN** `export.otlp.headers` holds `X-Scope-OrgID: tenant-a` and `Authorization: Bearer file`, and the `headers_env` variable holds `authorization=Bearer env`
- **THEN** OTLP push requests carry `x-scope-orgid: tenant-a` and `authorization: Bearer env`

#### Scenario: Percent-encoded value

- **WHEN** the `headers_env` variable holds `x-token=a%2Cb`
- **THEN** OTLP push requests carry `x-token: a,b`

#### Scenario: Inherited by self-telemetry

- **WHEN** a self-telemetry signal uses the `export.otlp` destination and `headers_env` supplies `authorization`
- **THEN** its exports carry that header

### Requirement: Header environment errors

The exporter SHALL NOT start when `export.otlp.headers_env` is set and the variable is unset, empty, or holds an entry that is not a valid `name=value` pair. The error SHALL name the variable and the entry position and SHALL NOT contain any header value.

#### Scenario: Variable not set

- **WHEN** `export.otlp.headers_env: OTLP_HEADERS` is configured and `OTLP_HEADERS` is unset or empty
- **THEN** the exporter exits at startup with an error naming `OTLP_HEADERS`

#### Scenario: Malformed entry

- **WHEN** the `headers_env` variable holds `authorization=Bearer s3cret,garbage`
- **THEN** the exporter exits at startup with an error naming the variable and entry 2, and the error does not contain `s3cret`

### Requirement: Header values never logged

The system SHALL NOT write any OTLP header value, from `export.otlp.headers` or `headers_env`, to a log line or error message.

#### Scenario: Push rejected

- **WHEN** an OTLP push carrying `authorization: Bearer s3cret` is rejected
- **THEN** no log line contains `s3cret`

### Requirement: Auth rejection warning

When the first OTLP push of the process is rejected as unauthenticated or forbidden (HTTP 401/403, gRPC `Unauthenticated`/`PermissionDenied`), the system SHALL log one warning that names `export.otlp.headers` and `export.otlp.headers_env`. `/healthz` and `/readyz` SHALL NOT change because of OTLP push failures.

#### Scenario: First push gets 401

- **WHEN** the first OTLP push gets HTTP 401
- **THEN** one auth-rejection warning is logged, `github_exporter.otlp.errors` is incremented, and `/healthz` stays 200

#### Scenario: Later rejections

- **WHEN** later pushes also get 401
- **THEN** no further auth-rejection warning is logged and each failure still increments `github_exporter.otlp.errors`
