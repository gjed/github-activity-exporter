# Proposal

## Why

The exporter reports nothing about itself beyond the `github_exporter.*` self-metrics: no runtime metrics, traces, OTLP logs or profiles ([#73](https://github.com/gjed/github-activity-exporter/issues/73)). Those signals land in [#74](https://github.com/gjed/github-activity-exporter/issues/74)–[#77](https://github.com/gjed/github-activity-exporter/issues/77) and each costs backend capacity, so they need one opt-in config surface, OTel-standard env var handling, a shared resource and provider wiring first.

## What Changes

- New `telemetry` config section: `telemetry.enabled` (default `false`) and `telemetry.{metrics,traces,logs,profiles}.enabled` (default `true`). A signal is active only when both are true. Each key has a `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_*` env override; invalid values fail startup with an error naming the key.
- Standard OTel SDK env vars for self-telemetry, with spec precedence over the file: `OTEL_EXPORTER_OTLP_{,TRACES_,METRICS_,LOGS_}{ENDPOINT,HEADERS,PROTOCOL,TIMEOUT}`, `OTEL_{TRACES,METRICS,LOGS}_EXPORTER` (`none` turns the signal off), `OTEL_TRACES_SAMPLER{,_ARG}`, `OTEL_RESOURCE_ATTRIBUTES`, `OTEL_SERVICE_NAME`, `OTEL_SDK_DISABLED`. With no OTLP endpoint in the environment, a signal exports to `export.otlp` (endpoint, protocol, headers, TLS).
- `OTEL_SDK_DISABLED=true` and `OTEL_METRICS_EXPORTER=none` turn off self-telemetry only. Product metrics, including the `github_exporter.*` self-metrics, are never affected by any `telemetry` key or `OTEL_*` toggle.
- Self-telemetry gets its own SDK providers (TracerProvider, LoggerProvider, MeterProvider), built only for active signals; inactive signals use no-op providers and open no connection. The self-telemetry MeterProvider also feeds `/metrics` when the Prometheus bridge is on.
- One resource for every provider, product metrics included: `service.name` (default `github-activity-exporter`, overridable by `OTEL_SERVICE_NAME`/`OTEL_RESOURCE_ATTRIBUTES`), `service.version` (build version), `service.instance.id` (UUID per process unless set in `OTEL_RESOURCE_ATTRIBUTES`), plus the existing detector attributes. Declared in the registry.
- One instrumentation scope, `github.com/gjed/github-activity-exporter`, versioned with the build version, for every meter, tracer and logger. The per-target scope names on product meters (`github.com/gjed/github-activity-exporter/<target>`) go away.
- Graceful shutdown flushes and shuts down every provider on SIGTERM/SIGINT.
- An exported `internal/telemetry` API (active-signal checks, scoped providers) that #74–#77 plug into. No signal is emitted by this change.
- README "Self-telemetry" section.

## Capabilities

### New Capabilities

- `self-telemetry`: opt-in telemetry about the exporter itself: config toggles, OTel env var handling, provider lifecycle, isolation from product metrics.

### Modified Capabilities

- `metric-export`: product metrics share the self-telemetry resource and one versioned instrumentation scope; `OTEL_SERVICE_NAME`/`OTEL_RESOURCE_ATTRIBUTES` override the default `service.name`.
- `weaver-schema`: the resource attribute group declares `service.version` and `service.instance.id`, and the default `service.name` scenario allows the env override.

## Impact

- Code: new `internal/telemetry`; `internal/config` (telemetry section), `internal/exporter/provider.go` (resource and scope injected, Prometheus registerer exposed), `internal/exporter/selfmetrics.go`, `internal/exporter/error_counting.go`, `cmd/github-activity-exporter/main.go`.
- Dependencies: `go.opentelemetry.io/otel/trace`, `otel/sdk/trace`, `otel/log`, `otel/sdk/log`, `otlptrace{http,grpc}`, `otlplog{http,grpc}`; `github.com/google/uuid` becomes direct.
- Registry: `registry/attributes.yaml` resource group; regenerated `internal/semconv/*.go` (no constant change expected).
- `/metrics` and OTLP product series: `otel_scope_name` changes from the per-target name to the single scope, and the resource gains `service.version` and `service.instance.id`.
- Docs: `README.md`.
