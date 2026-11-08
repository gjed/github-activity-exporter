# Proposal

## Why

`collection_interval` is applied as a pause after each cycle, so a cycle starts every cycle duration + `collection_interval`; on a 389-repo org with `collection_interval: 15m` and 17-minute cycles that is one cycle every 32 minutes ([#95](https://github.com/gjed/github-activity-exporter/issues/95)). Users size the key from their rate-limit budget and the freshness they want, and both silently get worse as the org grows. Separately, `Discoverer.Start` is only called from tests, so `discovery.refresh_interval` is dead config and repos created after startup are never collected until a restart ([#97](https://github.com/gjed/github-activity-exporter/issues/97)).

## What Changes

- `collection_interval` becomes a fixed period measured from the previous cycle start. A cycle that overruns the period is followed by the next one at once: cycles never overlap and missed periods are not caught up.
- Every overrun logs a warning and increments a new self-metric, `github_exporter.scrape.overruns`, declared in the registry with `annotations.prometheus` and generated with `make generate`.
- The `health.stall_timeout` default stays `2 × collection_interval + 1h`, re-derived for the new semantics in the design; the `/healthz` stall check is unchanged. Specs and README state the formula and why it still holds.
- README: `collection_interval` documented as the period between cycle starts; overrun behaviour described.
- The discoverer's background refresh runs in production, started in `buildTargetRunner` with the context the runner runs under. It no longer performs its own initial discovery (the runner's first `Repos()` call does), so startup lists repos once.
- A failed refresh keeps the last good repo list, is logged, and is counted in the existing `github_exporter.api.errors` with `github.api.endpoint=rest.repos`, like a failed discovery in a cycle. Repos that disappear from discovery stop being collected from the next cycle.
- README: one line documenting the per-target `discovery.refresh_interval` key (default 60m), which this change makes effective.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `metric-export`: "Configurable collection interval" becomes a fixed period with overrun behaviour; "Exporter self-metrics" gains `github_exporter.scrape.overruns`.
- `health-endpoints`: "Liveness of collection loops" states why the stall-timeout default holds under the fixed period.
- `repo-discovery`: "Incremental refresh" covers the production refresh, failed refreshes and removed repos.

## Impact

- Code: `internal/collector/runner.go` (`Run` loop, `SelfMetricsRecorder.RecordOverrun`), `internal/exporter/selfmetrics.go`, `internal/discovery/discoverer.go` (`Start` without initial discovery, refresh-failure hook), `cmd/github-activity-exporter/main.go` (`buildTargetRunner` starts the refresh), `internal/config/config.go` (doc comments only).
- Registry and generation: `registry/signals.yaml`, generated `internal/semconv/*.go`. The dashboard template is hand-curated and gets no new panel; the generated dashboard does not change.
- Tests: fake-clock tests with `testing/synctest` in `internal/collector` and `internal/discovery`; integration live-check emits the new metric.
- Docs: README configuration and health sections.
- Overlap: H1/H2 may touch `cmd/github-activity-exporter/main.go`; rebase on `origin/main` before the PR.
