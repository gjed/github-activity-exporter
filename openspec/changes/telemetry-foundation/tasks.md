# Tasks

## 1. Config

- [ ] 1.1 Add the `telemetry` section (`enabled` default false, per-signal `enabled` default true) with viper defaults for every key in `internal/config/config.go`; non-boolean values fail `Load` with an error naming the key; tests for defaults, file values, `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_*` overrides and invalid values; verify `go test ./internal/config/...`

## 2. Resource, scope and registry

- [ ] 2.1 Add `internal/telemetry` with `ScopeName` and `NewResource(ctx, version)` (defaults, detectors, then `WithFromEnv`); tests for the default resource and `OTEL_SERVICE_NAME`/`OTEL_RESOURCE_ATTRIBUTES` precedence; verify `go test ./internal/telemetry/...`
- [ ] 2.2 Add `service.version` and `service.instance.id` (required) to `github_activity_exporter.resource` in `registry/attributes.yaml`; verify `make check-schema` passes and `make generate` leaves no unexpected diff
- [ ] 2.3 Inject the resource and versioned scope into `internal/exporter` (`Config.Resource`, `Config.Version`; self-metrics, OTLP error counter) and `main.go` product meters (one scope, no per-target name); expose the bridge's Prometheus registerer; update exporter and main tests; verify `go test ./internal/exporter/... ./cmd/...`

## 3. Self-telemetry providers

- [ ] 3.1 Implement signal resolution (toggles, `OTEL_SDK_DISABLED`, `OTEL_<SIGNAL>_EXPORTER`) as a pure function with injectable `getenv`; table tests for the full toggle matrix, `none`, unsupported values and env-cannot-enable; verify `go test ./internal/telemetry/...`
- [ ] 3.2 Implement OTLP destination resolution (env endpoint → env-only; else inherit `export.otlp` with header env override, HTTP base-path rule, gRPC host:port) and build trace, metric (plus optional Prometheus reader without target/scope info) and log providers for active signals only; set global TracerProvider/LoggerProvider when active; httptest/gRPC-free tests: spans and log records reach a test OTLP/HTTP server with inherited headers, signal-specific endpoint wins, env endpoint drops file headers, sampler from env, no provider and no request when inactive; verify `go test ./internal/telemetry/...`
- [ ] 3.3 Implement `Active`, the provider accessors, `Meter`/`Tracer`, and `Shutdown` (meter, tracer, logger order); test that buffered spans are exported on `Shutdown`; verify `go test ./internal/telemetry/...`

## 4. Wiring

- [ ] 4.1 Wire `telemetry.New` in `main.go` after the product provider, log active signals at startup, shut it down after the product provider in the existing 30 s budget; verify `go build ./...`
- [ ] 4.2 Add an isolation test: for every toggle/env combination, product metrics reach the product OTLP endpoint and `/metrics` unchanged, and self-telemetry metrics appear on `/metrics` only when active, with one `target_info`; verify `go test ./...`

## 5. Documentation

- [ ] 5.1 Add the README "Self-telemetry" section (config block, activation rule, env var table with precedence, `OTEL_SDK_DISABLED` scope, always-on `github_exporter.*`, `service.instance.id` note, product exporter env caveat); verify markdown lint passes on `README.md`

## 6. Integration

- [ ] 6.1 Run `go build ./... && go test ./... && make check-schema && make generate` (no diff) and `make test-integration` (OTLP and `/metrics` live-checks); verify all pass

## Workflow follow-up

- Archive the change with `openspec archive telemetry-foundation` before the final commit.
