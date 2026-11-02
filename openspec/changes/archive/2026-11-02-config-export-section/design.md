# Design

## Context

`internal/exporter.Config` already has every field the new section needs (`OTLPEndpoint`, `Protocol`, `Headers`, `TLSInsecure`, `CollectionInterval`, `PrometheusEnabled`, `PrometheusAddr`) and applies its own defaults. Only the source of the values changes: `cmd/renovate-exporter/main.go` fills them from env vars today. `config.Load` uses viper, unmarshals with mapstructure, then runs `resolveEnvVars`, `compileFilters` and `validate`.

## Goals / Non-Goals

**Goals:**

- Config file is the only source for export settings.
- Invalid export config fails in `config.Load`, before any GitHub client or OTLP provider is built.

**Non-Goals:**

- No change to `internal/exporter` behaviour or its `Config` struct.
- No new TLS options beyond `insecure` (CA file, client certs). Add when someone needs them.
- No change to `CONFIG_FILE` or `RENOVATE_BOT_LOGIN`.

## Decisions

- **Types live in `internal/config`, mapped in `main.go`.** New `Export`, `OTLP`, `TLS`, `Prometheus` structs on `config.Config`, plus `CollectionInterval time.Duration`. `main.go` copies them into `exporter.Config`. Alternative: have `config` import `exporter` types directly. Rejected: keeps the config package free of exporter dependencies, matching how `Target` is mapped to the GitHub client today.
- **Defaults via `viper.SetDefault`** for `collection_interval` (`5m`), `export.otlp.protocol` (`http`) and `export.prometheus.listen_address` (`:9090`), so the loaded struct reflects effective values and tests can assert them. `exporter.Config.defaults()` stays as a second line of defence.
- **Validation in `validate`**: empty `export.otlp.endpoint` → error `export.otlp.endpoint is required`; protocol not in {`http`, `grpc`} → error; `collection_interval <= 0` → error. Duration parsing errors already surface from viper's unmarshal hook.
- **`tls.insecure` maps to the existing `TLSInsecure`** with its current per-protocol meaning (HTTP: skip verification; gRPC: plaintext connection). Documented as such in the README.
- **Listen address key is `listen_address`**, mirroring `PrometheusAddr` and the Prometheus `listen-address` convention.

## Risks / Trade-offs

- \[viper lower-cases map keys, so `X-Scope-OrgID` is sent as `x-scope-orgid`\] → HTTP header names are case-insensitive and gRPC metadata keys are lower-case by definition. Noted in the README.
- [Existing deployments break on upgrade: no endpoint in the file → exit 1] → Clear error message naming the key; the migration table below maps each old env var to its key.
- [Secrets such as an auth header now sit in the config file] → Same as `auth.token` today; users can still mount the file from a secret. An env indirection for headers is not requested and is left out.

## Migration Plan

Users move each removed env var into the config file:

| Old env var           | New key                             |
| --------------------- | ----------------------------------- |
| `OTLP_ENDPOINT`       | `export.otlp.endpoint`              |
| `OTLP_PROTOCOL`       | `export.otlp.protocol`              |
| `OTLP_AUTH_HEADER`    | `export.otlp.headers.Authorization` |
| `OTLP_TLS_INSECURE`   | `export.otlp.tls.insecure`          |
| `COLLECTION_INTERVAL` | `collection_interval`               |
| `PROMETHEUS_ENABLED`  | `export.prometheus.enabled`         |
| `PROMETHEUS_ADDR`     | `export.prometheus.listen_address`  |

Rollback: deploy the previous image with the env vars set.
