# Proposal: export cadence, one target_info writer, scrape duration buckets, api.errors initialisation

## Why

The GitHub metrics are pushed once per `collection_interval`, so with a long interval every series has gaps and `rate()` panels are empty most of the time ([#89](https://github.com/gjed/github-activity-exporter/issues/89)). Product and inherited self-telemetry metrics push the same `target_info` series on two schedules, which forces an out-of-order window on the backend ([#99](https://github.com/gjed/github-activity-exporter/issues/99)). `github_exporter.scrape.duration` buckets stop at 300 s, so long cycles all land in `+Inf` ([#94](https://github.com/gjed/github-activity-exporter/issues/94)). `github_exporter.api.errors` has no series until the first error, so `increase()` misses it after every restart ([#103](https://github.com/gjed/github-activity-exporter/issues/103)).

## What Changes

- New `export.interval` (default `60s`, must be positive) drives the product OTLP periodic reader. Every push re-sends the latest snapshot, so series stay continuous whatever `collection_interval` is. The per-cycle flush stays. The push keeps cumulative temporality, so repeated pushes do not double-count counters or histograms.
- Dashboard: the hard-coded panel Min interval `10m` becomes a constant dashboard variable `$min_step` (default `2m`, twice the default `export.interval`) used by every panel. The Helm chart gets `dashboard.minStep` to override it.
- When self-telemetry metrics inherit `export.otlp` (no `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` / `OTEL_EXPORTER_OTLP_ENDPOINT`), they ride in the product push instead of a second OTLP exporter: one writer for `target_info`, pushed every `export.interval`; `OTEL_METRIC_EXPORT_INTERVAL` does not apply. With an env endpoint the separate exporter stays. This reverses the [#73](https://github.com/gjed/github-activity-exporter/issues/73) decision.
- README and e2e drop the out-of-order ingestion requirement if the measured `target_info` timestamps are strictly increasing on a stock Prometheus 3.x; otherwise they keep it and say why. The README still warns that an env endpoint on the same backend brings back two writers.
- `github_exporter.scrape.duration` buckets become `1, 5, 10, 30, 60, 120, 300, 600, 900, 1800, 3600` s.
- `github_exporter.api.errors` gets a `0` per target × `github.api.endpoint` value when a target's runner starts, next to the existing `github_exporter.scrape.overruns` initialisation from [#100](https://github.com/gjed/github-activity-exporter/pull/100). The endpoint values come from a generated per-enum value list in `internal/semconv`, not a hand-written list. The registry enum has `graphql` and `rest.repos`; #103 also names `rest.issues`, which is not in the registry and is not added.
- `github_exporter.otlp.errors` is not initialised: its failures are counted by the OTLP exporter wrapper under one process-wide `exporter.target` value on a lazily created instrument, not per target at runner start, so the #100 pattern does not apply.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `metric-export`: OTLP push on `export.interval` plus per-cycle flush; collection interval no longer sets the push cadence; scrape duration buckets; `api.errors` initialised to 0.
- `self-telemetry`: inherited self-telemetry metrics travel in the product push; runtime metrics follow the push that carries them.
- `weaver-schema`: dashboard windows sized from `$min_step` and `export.interval` instead of `collection_interval`; generated enum value lists.
- `helm-chart`: dashboard ConfigMap honours `dashboard.minStep`.

## Impact

- Code: `internal/config` (`export.interval`), `internal/exporter/provider.go`, `config.go`, `selfmetrics.go`, `internal/telemetry/telemetry.go`, `internal/collector/runner.go`, `cmd/github-activity-exporter/main.go`.
- Generated: `templates/go/attributes.go.j2` → `internal/semconv/attributes.go`; `templates/grafana/dashboard.json.j2` → `dashboards/github-activity.json` and the chart copy.
- Chart: `values.yaml`, `templates/dashboard.yaml`, chart README.
- Docs: README (config, self-telemetry, backend requirements).
- Tests: unit tests for the reader cadence, cumulative re-push, producer attachment, buckets and api.errors init; e2e on a stock Prometheus 3.x with `collection_interval: 15m`.
- Behaviour: more OTLP pushes (every 60 s by default) with the same content; histogram bucket boundaries change, so old and new `scrape.duration` buckets differ across the upgrade.
