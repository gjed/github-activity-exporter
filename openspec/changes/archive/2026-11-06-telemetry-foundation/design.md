# Design

## Context

`internal/exporter.New` builds one MeterProvider (OTLP PeriodicReader plus the optional Prometheus bridge reader), sets it as the global MeterProvider, and builds its resource with `WithFromEnv`, the process/os/container/host detectors and a hard-coded `service.name` that, being applied last, overrides `OTEL_SERVICE_NAME`. Product meters are created per target with scope `github.com/gjed/github-activity-exporter/<target>`; self-metrics and the OTLP error counter use `github.com/gjed/github-activity-exporter`. Neither scope carries a version. `main.go` shuts the provider down on SIGTERM/SIGINT with a 30 s budget.

The Go SDK reads most `OTEL_*` variables natively: OTLP exporters read `OTEL_EXPORTER_OTLP_*` as defaults that explicit options override, `sdk/trace` reads `OTEL_TRACES_SAMPLER{,_ARG}` when no sampler is passed, `resource.WithFromEnv` reads `OTEL_RESOURCE_ATTRIBUTES`/`OTEL_SERVICE_NAME`. It does not implement `OTEL_SDK_DISABLED` or `OTEL_{TRACES,METRICS,LOGS}_EXPORTER`; the exporter does that itself.

## Goals / Non-Goals

**Goals:**

- One config surface and one env var resolution for all four self-telemetry signals, testable without a network.
- Self-telemetry can never change what product metrics export, whatever its config or env.
- A stable API that #74–#77 consume without touching config, resource or shutdown code.

**Non-Goals:**

- Any instrument, span, log record, bridge or pprof endpoint (#74–#77).
- Context propagation (`OTEL_PROPAGATORS`), exemplars, `http/json` OTLP, file-based declarative config (`OTEL_CONFIG_FILE`).
- Changing how the product OTLP exporter reads `OTEL_EXPORTER_OTLP_*` (see Risks).

## Decisions

### D1. Separate SDK providers for self-telemetry

Self-telemetry gets its own MeterProvider, TracerProvider and LoggerProvider in `internal/telemetry`. The product MeterProvider stays in `internal/exporter`, stays the global MeterProvider, and is built exactly as today apart from the shared resource and scope.

Why: turning self-telemetry on or off, a broken self-telemetry endpoint, `OTEL_METRICS_EXPORTER=none` or `OTEL_METRIC_EXPORT_INTERVAL` can then never change the product pipeline, its push cadence (one flush per cycle) or its error counting. Runtime metrics (#74) want the SDK's periodic interval, not the collection interval, which a shared reader could not give.

Alternative: register self-telemetry instruments on the product MeterProvider. Rejected: the toggle would become a view/filter on a shared pipeline, OTLP pushes would carry runtime metrics only at cycle end, and a self-telemetry endpoint override would be impossible.

When the Prometheus bridge is on, the self-telemetry MeterProvider gets a second Prometheus reader registered on the bridge's registry, with `WithoutTargetInfo` and `WithoutScopeInfo` so the shared registry exposes `target_info` once. #74 requires `go.*`/`http.*` on `/metrics` with the signal on.

### D2. `OTEL_SDK_DISABLED` scoped to self-telemetry

`OTEL_SDK_DISABLED=true` (case-insensitive) makes every self-telemetry signal inactive, as if `telemetry.enabled` were false, and logs one info line saying so. Product metrics keep exporting.

Why: the spec defines the variable as disabling the SDK for all signals, but the product metrics are the exporter's function, not optional instrumentation; honouring it literally would turn the process into one that collects and drops everything. Ignoring it entirely would surprise operators who set it fleet-wide to silence instrumentation. Scoping it to self-telemetry keeps its intent (no instrumentation overhead or egress) without breaking the product. Documented in the README.

### D3. `github_exporter.*` self-metrics stay always on

`github_exporter.scrape.duration`, `.api.errors`, `.otlp.errors`, `.api.rate_limit.*` stay on the product MeterProvider, unaffected by `telemetry.*`.

Why: the generated dashboard (its `job` variable is `label_values(github_exporter.scrape.duration, job)`) and the health semantics depend on them; they are declared product contract in the registry; they cost a handful of series per target. Gating them would break existing dashboards for anyone who keeps the default `telemetry.enabled: false`.

### D4. Toggle and env resolution

A signal is active when all hold: `telemetry.enabled`, `telemetry.<signal>.enabled`, `OTEL_SDK_DISABLED` is not true, and `OTEL_<SIGNAL>_EXPORTER` (traces, metrics, logs) is not `none`. Env vars can only switch a signal off; they never switch on a signal the file disables. `OTEL_<SIGNAL>_EXPORTER` accepts `otlp` (default) and `none`; any other value (for example `console`, `prometheus`) is logged as a warning and treated as `otlp`, per the spec rule to warn and ignore unsupported values. Profiles have no standard exporter variable; only the file toggles apply.

Resolution is a pure function of the config and a `getenv` func, so the toggle matrix and env precedence are table tests.

### D5. OTLP destination per signal

For each active OTLP signal (traces, metrics, logs):

- If `OTEL_EXPORTER_OTLP_<SIGNAL>_ENDPOINT` or `OTEL_EXPORTER_OTLP_ENDPOINT` is set, the signal is configured from the environment only, through the SDK exporter's native env handling (signal-specific over generic, generic endpoint gets `/v1/<signal>` appended for HTTP). Transport follows `OTEL_EXPORTER_OTLP_{<SIGNAL>_,}PROTOCOL` (`grpc` or `http/protobuf`, default `http/protobuf` per spec; `http/json` warns and falls back to `http/protobuf`). Nothing from `export.otlp` is applied, so product credentials never go to a different backend.
- Otherwise the signal inherits `export.otlp`: endpoint, protocol and TLS from the file; headers from the file unless `OTEL_EXPORTER_OTLP_{<SIGNAL>_,}HEADERS` is set; timeout from `OTEL_EXPORTER_OTLP_{<SIGNAL>_,}TIMEOUT` or the SDK default. For HTTP the file URL is taken as a base: a trailing `/v1/metrics` is stripped, then `/v1/<signal>` is appended (`https://gw/otlp` and `https://gw/otlp/v1/metrics` both give `https://gw/otlp/v1/traces`), matching how the spec derives signal URLs from `OTEL_EXPORTER_OTLP_ENDPOINT`. For gRPC the `host:port` is used as is.
- Sampler: no sampler option is passed, so `sdk/trace` applies `OTEL_TRACES_SAMPLER{,_ARG}` (default `parentbased_always_on`).

Alternative: resolve each setting independently (env > file > default). Rejected: an env endpoint for a separate backend combined with file headers would send the product backend's auth headers there.

### D6. Shared resource

`telemetry.NewResource(ctx, version)` builds the one resource, passed to the product MeterProvider and all self-telemetry providers. Order, lowest precedence first: defaults (`service.name=github-activity-exporter`, `service.version=<build version>`, `service.instance.id=<UUIDv4>`), the existing process/os/container/host detectors, then `WithFromEnv`, so `OTEL_RESOURCE_ATTRIBUTES` and `OTEL_SERVICE_NAME` win per spec. The UUIDv4 follows the semconv recommendation for `service.instance.id` when no stable identity is known; deployments that want a stable one (for example the pod name) set it in `OTEL_RESOURCE_ATTRIBUTES`.

The registry group `github_activity_exporter.resource` adds `service.version` (required) and `service.instance.id` (required), both referenced from the OTel semconv dependency.

### D7. One versioned instrumentation scope

`telemetry.ScopeName = "github.com/gjed/github-activity-exporter"` with `metric.WithInstrumentationVersion(version)` (and the trace/log equivalents) for every meter, tracer and logger, product meters included. The build `version` (ldflags; `dev` locally) is the module version: `debug.ReadBuildInfo` reports `(devel)` for the main module in our builds.

Product meters move from per-target scopes to the single scope. Each runner still registers its own callback; instruments with identical name and options from one meter are the same instrument in the SDK, and series stay distinct through `exporter.target`. The target is data, not instrumentation identity, which is why the per-target scope is wrong per the SDK conventions.

### D8. Hook API for #74–#77

```go
package telemetry

const ScopeName = "github.com/gjed/github-activity-exporter"

type Signal string // SignalMetrics, SignalTraces, SignalLogs, SignalProfiles

func NewResource(ctx context.Context, version string) (*resource.Resource, error)
func New(ctx context.Context, s Settings) (*Telemetry, error)

func (t *Telemetry) Active(s Signal) bool
func (t *Telemetry) MeterProvider() metric.MeterProvider   // noop when inactive
func (t *Telemetry) TracerProvider() trace.TracerProvider  // noop when inactive
func (t *Telemetry) LoggerProvider() log.LoggerProvider    // noop when inactive
func (t *Telemetry) Meter() metric.Meter                   // ScopeName + version
func (t *Telemetry) Tracer() trace.Tracer                  // ScopeName + version
func (t *Telemetry) Shutdown(ctx context.Context) error
```

`Settings` carries the `telemetry` config, `export.otlp`, the resource, the version, the optional Prometheus registerer, a logger and `getenv` (defaults to `os.Getenv`; tests inject).

Consumers: #74 passes `MeterProvider()` to the runtime instrumentation and `otelhttp.WithMeterProvider`; #75 passes `TracerProvider()` to `otelhttp` and uses `Tracer()`; #76 passes `LoggerProvider()` to `otelslog`; #77 checks `Active(SignalProfiles)` to mount pprof. When traces are active, `New` sets the global TracerProvider; when logs are active, the global LoggerProvider. The global MeterProvider stays the product one.

### D9. Shutdown

After the runners stop, `main` shuts down the product provider, then `Telemetry.Shutdown`, sharing the existing 30 s context. `Telemetry.Shutdown` flushes and shuts down meter, tracer, then logger providers (logger last so shutdown log records still go out), joining errors. Inactive signals have nothing to shut down.

### D10. Config surface

```yaml
telemetry:
  enabled: false
  metrics: { enabled: true }
  traces: { enabled: true }
  logs: { enabled: true }
  profiles: { enabled: true }
```

Every key gets a viper default so `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_ENABLED` and `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_<SIGNAL>_ENABLED` override it (viper only maps env vars onto known keys). A value that does not parse as a boolean fails `config.Load` with an error naming the key.

## Risks / Trade-offs

- \[`otel_scope_name` changes on every product series; `/metrics` scrapers see new series once\] → Called out in the PR; the dashboard does not use the label.
- \[`service.instance.id` changes on restart; OTLP backends that map it to `instance` start new series per restart\] → Documented; set a stable id via `OTEL_RESOURCE_ATTRIBUTES`. Gauges already overlap for one staleness window on restarts today.
- \[The product OTLP exporter reads `OTEL_EXPORTER_OTLP_{,METRICS_}*` (headers, compression, timeout, certificates, temporality, histogram aggregation) as defaults for every option it is not given, so self-telemetry variables would reach the product push\] → The product exporter passes every one of those options explicitly, from `export.otlp` or the SDK default, and the PeriodicReader pins its timeout against `OTEL_METRIC_EXPORT_TIMEOUT`.
- [Two Prometheus readers on one registry could collide on a family name] → Self-telemetry metric names (`go.*`, `http.*`) never overlap registry names; `target_info`/scope info come only from the product reader.
- \[`otel/log` and the OTLP log exporters are pre-1.0 (v0.x)\] → Pinned; only `New`/`Shutdown` touch them in this change.

## Migration Plan

Default config is unchanged in behaviour apart from the resource and scope changes above. Rollback is a revert; no state.
