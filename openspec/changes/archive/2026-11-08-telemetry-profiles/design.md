# Design

## Context

[#73](https://github.com/gjed/github-activity-exporter/issues/73) added `telemetry.profiles.enabled` (default `true`) and the activation rule (`telemetry.enabled`, the signal's own toggle, `OTEL_SDK_DISABLED`; profiles have no `OTEL_PROFILES_EXPORTER`), but no provider: `internal/telemetry.New` resolves `SignalProfiles` and builds nothing, and `main.go` leaves profiles out of the `self_telemetry` startup log.

State of OTel profiles (2026-11): the signal is public Alpha, OTLP profiles are `v1development`, the Collector needs the `service.profilesSupport` feature gate, and the OTel Go SDK has no profiles API. The Collector contrib `pprof` receiver (alpha, in `otelcol-contrib` v0.165.0) has a `server` mode that accepts `POST /v1/pprof` with a gzip-encoded `profile.proto` body and answers `204`, then emits OTLP profiles.

## Goals / Non-Goals

**Goals:**

- Push profiles of the exporter process to an OpenTelemetry Collector when the signal is enabled, at no cost when it is not.
- No inbound debug endpoint, no new dependency.
- A broken profiles setting never stops the exporter.

**Non-Goals:**

- A `/debug/pprof/` pull endpoint.
- OTLP profiles produced in-process.
- Auth or TLS options for the profiles push (the Collector is expected in-cluster; added when needed).
- Span-to-profile linking: belongs to traces ([#75](https://github.com/gjed/github-activity-exporter/issues/75)).
- Helm template changes.

## Decisions

### D1. Push pprof to the Collector `pprof` receiver

`runtime/pprof` produces gzip-compressed `profile.proto`; the exporter POSTs it to `<endpoint>/v1/pprof` with `Content-Type: application/octet-stream` and `Content-Encoding: gzip`. The Collector converts it to OTLP profiles and forwards it with its own pipeline (`otlp_grpc`/`otlp_http` to Pyroscope, Grafana Cloud, or `debug`).

Alternatives:

- Grafana Pyroscope Go SDK. Rejected: vendor ingest protocol, a new dependency, and it cannot target a Collector.
- `net/http/pprof` on the health server, scraped by Alloy `pyroscope.scrape` or the `pprof` receiver `remote` mode (the original #77 design). Rejected: pull needs an inbound debug endpoint kept cluster-internal.
- OTLP profiles exporter in-process. Not possible: the Go SDK has no profiles API.
- OTel eBPF profiler only. Needs no exporter code and is documented as an alternative, but it is a privileged node-wide DaemonSet, not something the exporter can turn on.

### D2. Configuration

```yaml
telemetry:
  enabled: true
  profiles:
    enabled: true                 # default false
    endpoint: http://otel-collector.monitoring.svc:4040
    interval: 60s
    mutex_fraction: 0             # runtime.SetMutexProfileFraction
    block_rate: 0                 # runtime.SetBlockProfileRate, nanoseconds
```

- `profiles.enabled` defaults to `false`. Before this change it defaulted to `true` but had no effect, so no deployment loses profiles.
- Every new key gets a viper default so `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_PROFILES_*` overrides work like the existing toggles.
- No `export.otlp` inheritance: the `pprof` receiver is a different port and protocol from the Collector's OTLP receiver.

### D3. Configuration errors are logged, not fatal

Profiles validation runs in `internal/telemetry` (not `config.validate`, which is fatal) and only when the signal is active: endpoint is an http(s) URL with a host and no query, `interval > 0`, `mutex_fraction >= 0`, `block_rate >= 0`. Any failure logs one error per bad key at `ERROR` and leaves the signal inactive; the exporter starts and exports GitHub metrics. A type error that viper cannot decode (for example `interval: abc`) is still a config load error, as for every other key.

### D4. Push loop

One goroutine per process. Each interval: `pprof.StartCPUProfile` into a buffer, wait for the interval (or shutdown), `StopCPUProfile`, push it; then push `heap` (in-use and allocated since start), `goroutine`, and `mutex`/`block` when their rates are above `0`, each from `pprof.Lookup(name).WriteTo(w, 0)`. Heap, mutex and block are cumulative since process start, as `runtime/pprof` reports them; the backend diffs them. Pushes use a dedicated `http.Client` with a 30 s timeout. A failed push (transport error or non-2xx) logs an `ERROR` with the profile type and cause and is not retried; the next interval pushes fresh profiles.

The runtime rates are set before the loop starts and reset to `0` when it stops. They are off by default because both add overhead on every contended lock or blocking event. CPU profiling samples at 100 Hz for the whole time the signal is active.

Labels: pprof has no resource attributes, so the Collector sets them (a `resource` processor with `service.name`, or `k8s_attributes`). Documented in the Collector snippet.

### D5. Lifecycle

`telemetry.New` starts the loop when the signal is active and the settings are valid. `Telemetry.Shutdown` stops it last, after logs: the current CPU window is ended and pushed, bounded by the shutdown context.

### D6. Moving to OTLP profiles in-process

Replace the pprof push with an in-process OTLP profiles exporter once the OTel Go SDK ships a stable profiles API (a profiles SDK marked Stable) and the target backends accept that OTLP profiles version. The `telemetry.profiles` toggle stays; the destination would then follow telemetry-foundation D5 (inherit `export.otlp` or `OTEL_EXPORTER_OTLP_PROFILES_*`) and `endpoint`/`interval` would be deprecated. The OTel eBPF profiler (`otelcol-ebpf-profiler`) remains the zero-code alternative.

## Risks / Trade-offs

- [The `pprof` receiver and OTLP profiles are alpha; the receiver's push API may change] → Isolated in `internal/telemetry`; README pins the tested Collector version.
- [Receiver only in `otelcol-contrib`, not in `otelcol-ebpf-profiler`] → Documented; the snippet uses `otel/opentelemetry-collector-contrib`.
- [Overhead: 100 Hz CPU sampling while active, a heap profile per interval] → Off by default; documented.
- [No auth on the push] → Collector expected in-cluster; non-goal for now.
