# Design

## Context

`config.Load` reads one YAML file through viper with `AutomaticEnv`, prefix `GITHUB_ACTIVITY_EXPORTER` and `.`→`_` key replacement. The product OTLP exporter passes `export.otlp.headers` with `WithHeaders` and pins every other option, so `OTEL_EXPORTER_OTLP_*` never reach it. Self-telemetry inherits `export.otlp` (headers included) unless an `OTEL_EXPORTER_OTLP_*ENDPOINT` is set. The chart renders `secret.env` entries first in the container `env` list, then `.Values.env` verbatim.

## Goals / Non-Goals

**Goals:**

- A token for the product OTLP push can live only in a Secret, including when the Secret holds the raw token and the backend needs `Authorization: Bearer <token>`.
- No header value in any log line or error.

**Non-Goals:**

- Changing how `OTEL_EXPORTER_OTLP_*` configure self-telemetry.
- Health semantics: `/healthz` and `/readyz` keep ignoring OTLP push failures.
- Fixing viper's handling of `GITHUB_ACTIVITY_EXPORTER_EXPORT_OTLP_HEADERS*` (see Risks).

## Decisions

### `export.otlp.headers_env` naming one env var

Chosen over per-header `GITHUB_ACTIVITY_EXPORTER_EXPORT_OTLP_HEADERS_<NAME>` overrides, which were tested against `config.Load` on main and don't work as a mechanism:

| Env var set                                          | Headers in file              | Result                               |
| ---------------------------------------------------- | ---------------------------- | ------------------------------------ |
| `..._HEADERS_AUTHORIZATION=Bearer from-env`          | none, or `{}`                | `map[]`: env ignored                 |
| `..._HEADERS_AUTHORIZATION=Bearer from-env`          | `x-scope-orgid: tenant`      | `map[x-scope-orgid:tenant]`: ignored |
| `..._HEADERS_AUTHORIZATION=Bearer from-env`          | `authorization: placeholder` | `map[authorization:Bearer from-env]` |
| `..._HEADERS_X_SCOPE_ORGID=from-env`                 | `x-scope-orgid: tenant`      | `map[x-scope-orgid:tenant]`: ignored |
| `..._EXPORT_OTLP_HEADERS=authorization=Bearer whole` | any                          | `map[]`: every file header dropped   |

Viper maps env onto keys it already knows, so an override needs a placeholder header in the ConfigMap, and the `-` in names like `x-scope-orgid` never matches the `_`-replaced key. `headers_env` follows the existing `token_env` / `private_key_env` pattern and takes the format users already know from `OTEL_EXPORTER_OTLP_HEADERS`.

### Format and parsing

Same as `OTEL_EXPORTER_OTLP_HEADERS` as the OTel Go SDK parses it: split on `,`, cut each entry at the first `=` (so base64 `=` padding survives), trim both sides, percent-decode the value. Names are lower-cased, matching what viper does to `export.otlp.headers` keys, so the merge compares like with like. Unlike the SDK, which skips bad entries and logs the raw value, any malformed entry (no `=`, invalid header name, bad percent-escape) fails `Load` with `export.otlp.headers_env: env var "X", entry N: <reason>`. The value is never part of the message.

### Merge precedence

`headers_env` headers are merged into `export.otlp.headers` at load time; for the same name the env var wins. Non-secret headers (`x-scope-orgid`) can stay in the ConfigMap and the token in the Secret. Resolving at load time means the product exporter and inherited self-telemetry see one map and no other code changes. Alternative: fail on overlap. Rejected: env-over-file is how every other `GITHUB_ACTIVITY_EXPORTER_*` override behaves.

Set but unset/empty variable is a startup error, same as `token_env`: a silent unauthenticated push is the bug this change fixes.

### Bearer prefix with no extra Secret

Kubernetes expands `$(VAR)` in an `env[].value` from vars defined earlier in the same list. The chart already renders `secret.env` before `env`, so:

```yaml
secret:
  env:
    OTLP_TOKEN: otlp-token
env:
  - name: OTLP_HEADERS
    value: authorization=Bearer $(OTLP_TOKEN)
config:
  export:
    otlp:
      headers_env: OTLP_HEADERS
```

No template change; the ordering becomes a spec guarantee. The example uses a variable name outside `OTEL_*` so the header does not also reach self-telemetry through the SDK's own env handling.

### Auth-rejection warning

In `errorCountingExporter`, on the first export that fails with an auth rejection, log one extra warning naming `export.otlp.headers` / `headers_env`. gRPC: `status.Code` is `Unauthenticated` or `PermissionDenied`. HTTP: the SDK returns a plain `fmt.Errorf` with `resp.Status` and no typed status, so it matches `" 401 Unauthorized"` / `" 403 Forbidden"` in the error text, marked `SIMPLIFIED:` with that ceiling. Existing per-failure warning and counter unchanged.

## Risks / Trade-offs

- \[`GITHUB_ACTIVITY_EXPORTER_EXPORT_OTLP_HEADERS` set to a string silently drops every file header (table above)\] → Out of scope; the README states the headers map cannot be overridden through `GITHUB_ACTIVITY_EXPORTER_*` and points to `headers_env`.
- \[A raw token containing `%` or `,` must be percent-encoded\] → Same rule as `OTEL_EXPORTER_OTLP_HEADERS`; documented.
- [HTTP auth detection depends on SDK error text] → Only affects the hint; the error itself is still logged and counted. A missed match costs one warning, never behavior.
- \[`$(VAR)` expansion silently leaves the literal `$(OTLP_TOKEN)` if the var is missing\] → The chart renders `secret.env` first; the example and README name the ordering requirement.

## Migration Plan

Users on the documented `OTEL_EXPORTER_OTLP_HEADERS` wiring get no auth on the product push today; they add `export.otlp.headers_env` and optionally rename the var. Rollback: drop the key.
