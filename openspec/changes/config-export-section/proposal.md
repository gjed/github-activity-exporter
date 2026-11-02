# Proposal

## Why

The `metric-export` spec defines the OTLP, Prometheus and collection-interval settings as config-file keys, and says the exporter MUST NOT start without an OTLP endpoint. The code reads them from env vars instead, silently defaults the endpoint to `http://localhost:4318`, and only supports a single `Authorization` header, so backends that need other headers (e.g. `X-Scope-OrgID`) cannot be used. Issue #15.

## What Changes

- Add an `export` section to the YAML config:
  - `export.otlp.endpoint` (required)
  - `export.otlp.protocol`: `http` (default) or `grpc`; any other value is a startup error
  - `export.otlp.headers`: arbitrary string map sent on every OTLP request
  - `export.otlp.tls.insecure`: opt-in to skip TLS verification (default `false`)
  - `export.prometheus.enabled` (default `false`) and `export.prometheus.listen_address` (default `:9090`)
- Add a top-level `collection_interval` (default `5m`).
- **BREAKING**: a missing `export.otlp.endpoint` is a fatal startup error. The `http://localhost:4318` default is removed.
- **BREAKING**: the env vars `OTLP_ENDPOINT`, `OTLP_PROTOCOL`, `OTLP_AUTH_HEADER`, `OTLP_TLS_INSECURE`, `COLLECTION_INTERVAL`, `PROMETHEUS_ENABLED` and `PROMETHEUS_ADDR` are no longer read.
- Update the README (quick start, config file example, env var table).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `metric-export`: the endpoint requirement gains a missing-endpoint and an invalid-protocol scenario, the Prometheus bridge gains a configurable listen address, and the headers/TLS requirement gains a TLS scenario.

## Impact

- `internal/config/config.go` and its tests/testdata: new types, defaults, validation.
- `cmd/renovate-exporter/main.go`: builds `exporter.Config` from the loaded config; env var reads and the `mustDuration` helper go away.
- `README.md`: config documentation.
- Users: existing deployments that set the OTLP env vars must move them into the config file before upgrading. The project is on 0.x, so this ships as a minor `feat` without a breaking-change marker.
- Out of scope: `GITHUB_TOKEN` fallback (#21), GitHub base URL (#22), health endpoints (#18).
