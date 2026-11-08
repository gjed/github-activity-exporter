# Tasks

## 1. Config

- [x] 1.1 Add `export.otlp.headers_env` to `internal/config`: parse the named var (`OTEL_EXPORTER_OTLP_HEADERS` format, lower-cased names, percent-decoded values), merge into `Export.OTLP.Headers` with env winning, fail on unset/empty var or malformed entry with an error naming the var and entry position only; table tests cover merge, precedence, percent-decoding, `=` in values, unset, empty, each malformed form, and assert no error contains the secret value; verify `go test ./internal/config/...`

## 2. Exporter

- [x] 2.1 Log one auth-rejection warning on the first push rejected with HTTP 401/403 or gRPC Unauthenticated/PermissionDenied (`SIMPLIFIED:` note on HTTP text matching); tests against an `httptest` OTLP server returning 401 assert exactly one warning across two pushes, the counter still increments per failure, and no captured log line contains the header value; verify `go test ./internal/exporter/...`

## 3. Docs and chart

- [x] 3.1 App README: `headers_env` in the config reference with format, precedence and errors; state `GITHUB_ACTIVITY_EXPORTER_*` cannot override `export.otlp.headers`; self-telemetry section states `OTEL_EXPORTER_OTLP_*` apply to self-telemetry only and inherited headers include `headers_env`; verify markdown lint on the changed file
- [x] 3.2 Chart README Secrets table and paragraph, `values.yaml` `secret.env` comment, `examples/renovate-values.yaml` (raw token + `$(OTLP_TOKEN)`), `examples/github-app-values.yaml` (full header string) move to `headers_env`; verify markdown lint, `helm lint` and `helm template` with every example, and that `OTLP_TOKEN` precedes `OTLP_HEADERS` in the rendered env

## 4. Integration

- [x] 4.1 `go build ./...`, `go test ./...`, `make check-schema`, `make generate` with no diff
- [x] 4.2 End to end: exporter against a local OTel Collector (docker) with `bearertokenauth`, token only in an env var, `headers_env` building `authorization=Bearer <token>`: pushes accepted, `github_exporter.otlp.errors` absent/0; then a wrong token: 401s, the auth warning once, the counter rising, `/healthz` 200

## Workflow follow-up

- Archive the change with `openspec archive otlp-headers-from-env` before the final commit of the PR.
