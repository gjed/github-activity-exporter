# Proposal

## Why

The Helm chart tells users to put OTLP auth in `OTEL_EXPORTER_OTLP_HEADERS`, which the GitHub metrics push ignores by design, so against an authenticated backend every push gets a 401 while the pod stays Ready ([#86](https://github.com/gjed/github-activity-exporter/issues/86)). The docs can't be fixed by pointing elsewhere: `export.otlp.headers` lives in the config ConfigMap, which must not carry tokens, and there is no supported way to read the product OTLP headers from a Secret.

## What Changes

- New config key `export.otlp.headers_env`: the name of an env var holding headers as comma-separated `name=value` pairs, the `OTEL_EXPORTER_OTLP_HEADERS` format. They are merged into `export.otlp.headers`; for the same header name the env var wins.
- `headers_env` set but the variable unset or empty, or a malformed entry, is a startup error naming the variable and the entry position, never the value. Header values are never logged.
- Self-telemetry that inherits `export.otlp` inherits the merged headers, as it does `export.otlp.headers` today.
- A one-time warning the first time an OTLP push is rejected with 401/403 (HTTP) or Unauthenticated/PermissionDenied (gRPC), pointing at `export.otlp.headers` and `headers_env`. `/healthz` and `/readyz` are unchanged.
- Helm chart: no template change. Docs and both examples move from `OTEL_EXPORTER_OTLP_HEADERS` to `headers_env`. `renovate-values.yaml` shows the raw-token case: the Secret holds only the token and an `env` entry builds `authorization=Bearer $(OTLP_TOKEN)` with Kubernetes dependent env vars, no extra Secret. `github-app-values.yaml` shows a Secret key holding the full header string.
- App README: `headers_env` in the config reference, a note that `GITHUB_ACTIVITY_EXPORTER_*` cannot override `export.otlp.headers`, and the self-telemetry section states that `OTEL_EXPORTER_OTLP_*` apply to self-telemetry only.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `metric-export`: OTLP headers can come from an env var named by `export.otlp.headers_env`, with merge precedence, startup errors that never reveal values, and a first-push auth-rejection warning.
- `helm-chart`: the "OTLP headers from the Secret" scenario is replaced by the `headers_env` wiring; `secret.env` vars come before `env` entries so an `env` entry can reference a Secret value with `$(VAR)`.

## Impact

- Code: `internal/config` (new key, parsing, merge), `internal/exporter/error_counting.go` (auth-rejection warning).
- Chart: `README.md`, `values.yaml` comment, `examples/renovate-values.yaml`, `examples/github-app-values.yaml`.
- Docs: `README.md`.
- No new dependency, no metric or schema change, no change to `/healthz` or `/readyz`.
