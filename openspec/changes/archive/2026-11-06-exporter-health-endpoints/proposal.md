# Proposal

## Why

The only `/healthz` lives on the opt-in Prometheus bridge server and always returns 200, so a default deployment has no liveness or readiness signal, and even with the bridge on a stuck loop or a revoked credential looks healthy. Orchestrator probes need real signals. [Issue #18](https://github.com/gjed/github-activity-exporter/issues/18).

## What Changes

- New always-on HTTP server serving `/healthz` and `/readyz`, independent of the Prometheus bridge. Listen address from the new config key `health.listen_address`, default `:10062`, the first unallocated port in the [Prometheus default port allocations](https://github.com/prometheus/prometheus/wiki/Default-port-allocations) list (9100–10061 are all taken).
- `/readyz` returns 200 once every target has passed its startup credential check and completed at least one collection cycle; 503 before that.
- `/healthz` returns 503 when a target's collection loop has exited or stalled, or when a target's credential has been rejected with HTTP 401 for N consecutive collection cycles. A 401 never affects `/readyz`.
- Thresholds are configurable: `health.stall_timeout` (default `2 × collection_interval + 1h`) and `health.unauthorized_cycles` (default `3`).
- The health server starts before the startup credential checks, so `/healthz` answers while the exporter starts.
- `health.listen_address` must not equal `export.prometheus.listen_address` when the bridge is enabled; startup fails otherwise.
- Remove `/healthz` from the Prometheus bridge server. It now serves only `/metrics`. The old handler returned 200 unconditionally and was never part of the spec.
- Update the README config reference and add a health endpoints section.

## Capabilities

### New Capabilities

- `health-endpoints`: liveness and readiness HTTP endpoints, their listen address, and the exact pass/fail criteria.

### Modified Capabilities

None. The bridge `/healthz` was never specified; `metric-export` requirements do not change.

## Impact

- New `internal/health` package: per-target state tracker and the `/healthz` + `/readyz` handlers.
- `internal/config/config.go`: `health.listen_address`, `health.stall_timeout`, `health.unauthorized_cycles`, defaults and validation.
- `internal/github`: a hook reporting, per GitHub HTTP response and per GitHub App installation-token exchange, whether GitHub answered 401.
- `internal/collector/runner.go`: reports cycle start and completion (and whether repo discovery succeeded) to an optional observer.
- `internal/exporter/provider.go`: drop the bridge `/healthz` handler.
- `cmd/github-activity-exporter/main.go`: start/stop the health server, wire the tracker into clients and runners.
- `README.md`: config reference and health endpoints section.
- Out of scope: Helm chart and Dockerfile `HEALTHCHECK` (the distroless image has no HTTP client), metrics about health, TLS on the health server.
