## 1. Config

- [ ] 1.1 Default `telemetry.profiles.enabled` to `false`; add `endpoint`, `interval` (`60s`), `mutex_fraction`, `block_rate` (`0`) with viper defaults
- [ ] 1.2 Tests for the defaults and env overrides

## 2. Profiler

- [ ] 2.1 Validate the profiles settings in `internal/telemetry` when active; log one error per bad key and keep the signal inactive
- [ ] 2.2 Push loop: CPU window per interval, heap, goroutine, mutex/block when enabled, to `<endpoint>/v1/pprof`; set and reset runtime rates
- [ ] 2.3 Stop the loop last in `Telemetry.Shutdown`, pushing the partial CPU window
- [ ] 2.4 Tests against an `httptest` server: pushes when active, none when inactive, headers and gzip body, mutex/block only when enabled, push errors logged, invalid settings logged and inactive, final push on shutdown
- [ ] 2.5 List `profiles` under `self_telemetry` in the startup log

## 3. Docs

- [ ] 3.1 README: profiles section (config, Collector snippet, overhead, eBPF profiler alternative, when to move to OTLP profiles in-process); update the defaults and remove the "#77" placeholders
- [ ] 3.2 Chart: `examples/profiles-values.yaml` and a chart README section
- [ ] 3.3 Markdown lint on the changed files

## 4. Validation

- [ ] 4.1 `go build ./...`, `go test ./...`, `make check-schema`, `make generate` without diff
- [ ] 4.2 Run the binary with a local `otel/opentelemetry-collector-contrib:0.165.0` (`pprof` receiver server mode, `debug` exporter): profiles on shows received profiles; profiles off shows none and `/debug/pprof/` 404; bad endpoint logs an error and the exporter keeps running
- [ ] 4.3 `helm lint` and `helm template` with the new example
- [ ] 4.4 Rewrite issue #77 to the push design
- [ ] 4.5 `openspec archive telemetry-profiles` before the final commit
