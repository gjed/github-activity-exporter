# Proposal

## Why

CPU and memory hotspots in the collectors (full pagination, GraphQL decoding, Dependency Dashboard parsing) can only be found today by rebuilding the exporter with ad-hoc profiling. The `telemetry.profiles` toggle from [#73](https://github.com/gjed/github-activity-exporter/issues/73) exists but does nothing ([#77](https://github.com/gjed/github-activity-exporter/issues/77)).

The OTel profiles signal is Alpha, OTLP profiles are `v1development`, and the OTel Go SDK has no profiles API. The OpenTelemetry Collector `pprof` receiver (contrib, alpha) accepts pprof pushed over HTTP and turns it into OTLP profiles, so the exporter can push profiles to a Collector with the standard library alone, without an inbound debug endpoint or a vendor SDK.

## What Changes

- When the profiles signal is active, the exporter pushes pprof profiles of its own process (CPU, heap, goroutine and, when enabled, mutex and block) to an OpenTelemetry Collector `pprof` receiver (`POST <endpoint>/v1/pprof`) every `telemetry.profiles.interval`.
- New `telemetry.profiles` keys: `endpoint`, `interval` (default `60s`), `mutex_fraction` and `block_rate` (both default `0`, off).
- `telemetry.profiles.enabled` now defaults to `false`: profiles are turned on explicitly.
- A missing or invalid profiles setting does not stop the exporter: it logs an error naming the key and runs with profiles off. Failed pushes are logged as errors.
- On shutdown the profiler pushes the current CPU window, within the existing deadline. The startup log lists `profiles` under `self_telemetry` when active.
- No `/debug/pprof/` endpoint and no `net/http/pprof` import. No new dependency.
- Docs: README profiles section with a ready-to-use Collector config (`pprof` receiver in server mode, a `debug` exporter, and an OTLP exporter to a profiles backend); the OTel eBPF profiler as the alternative path to OTLP profiles; when to move to an in-process OTLP profiles exporter. Helm chart: a values example enabling profiles plus a chart README section. No template changes.
- Issue #77 is rewritten to the push design.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `self-telemetry`: `profiles.enabled` defaults to `false`; profiles become a working signal (Collector push, configuration, non-fatal validation, overhead toggles, shutdown).

## Impact

- Code: `internal/config` (profiles keys, defaults, validation), `internal/telemetry` (profiler), `cmd/github-activity-exporter/main.go` (startup log).
- Dependencies: none.
- Docs: `README.md`, `charts/github-activity-exporter/README.md`, new `charts/github-activity-exporter/examples/profiles-values.yaml`.
- Network: one outbound HTTP connection to the Collector when active; none when inactive.
- Behaviour: configs that set `telemetry.enabled: true` without `telemetry.profiles.enabled` keep profiles off, as before (the toggle had no effect).
