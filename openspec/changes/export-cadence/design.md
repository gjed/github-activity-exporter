# Design: export-cadence

## Context

See proposal.md for motivation. Current state:

- `internal/exporter/provider.go`: one `PeriodicReader` with `WithInterval(cfg.CollectionInterval)`; each runner calls `Provider.ForceFlush` after its cycle. The SDK serialises ticks and flushes in the reader's own goroutine (`PeriodicReader.run`), and resets the ticker after a flush.
- Product exporters pin `metric.DefaultTemporalitySelector` (cumulative) and `DefaultAggregationSelector`, so `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE` cannot switch them to delta.
- `internal/telemetry/telemetry.go` `newMeterProvider`: self-telemetry metrics always get their own `PeriodicReader` + OTLP exporter (interval `OTEL_METRIC_EXPORT_INTERVAL`), plus a bridge reader `WithoutTargetInfo` when `/metrics` is on.
- `main.go` builds the product `Provider` first, then `telemetry.New` (which needs the bridge registerer from the provider).
- `SelfMetrics.InitOverruns` (from #100) adds 0 per target at the top of `Runner.Run`.

## Goals / Non-Goals

**Goals:** continuous series at any `collection_interval`; one `target_info` writer in the inherited case; scrape duration buckets that cover hour-long cycles; `api.errors` series present from the first push.

**Non-Goals:** changing collection scheduling, the Prometheus bridge, the env-endpoint self-telemetry path, `otlp.errors` initialisation, or adding `rest.issues` to the registry.

## Decisions

### D1. `export.interval` drives the product reader

`config.Export.Interval` (`export.interval`, default `60s`, `> 0` else fatal naming the key). `exporter.Config.CollectionInterval` is renamed `ExportInterval` and only feeds `metric.WithInterval`; `main.go` passes `cfg.CollectionInterval` to the runners directly. The per-cycle `ForceFlush` stays, so a cycle's data still leaves at cycle end.

Each tick calls `Collect`, which runs the observable callbacks; they return the runners' latest snapshots, so every push repeats the last values. Synchronous instruments (counters, histograms) are aggregated cumulatively and re-sent with the same start time, so N pushes of one `Add(1)` export `1` N times, never `N`. Proof: a unit test with an in-memory exporter behind a `PeriodicReader` records once and flushes several times, asserting the same sum, count and start time on every export; the e2e asserts `github_exporter.scrape.duration_count == 1` after many pushes with a single 15m cycle.

Alternative: keep one push per cycle and raise the dashboard window. Rejected: it only moves the threshold (#89) and breaks raw selectors and alerts.

No lower bound beyond `> 0`: a sub-second interval is the operator's choice. SIMPLIFIED: no warning for `export.interval > collection_interval`; that setting is harmless (the per-cycle flush still pushes).

### D2. Dashboard `$min_step`

A Grafana `constant` variable `min_step`, default `10m`, referenced as `"interval": "$min_step"` by every panel. Grafana's `$__rate_interval` = max(`$__interval` + scrape, 4 × scrape), so with the default 15 s data source scrape interval the window is 10m15s. Two lower bounds apply:

- 2 × `export.interval`: the window holds two pushes, so `rate()` and `last_over_time()` panels have a value whatever `collection_interval` is.
- 2 × `collection_interval`: the window holds a cycle, so `histogram_quantile(rate(_bucket))` panels have a new observation. Histograms only grow once per cycle; a window without one gives NaN.

An earlier draft used `2m` (2 × default `export.interval` only). The e2e showed the histogram panels NaN most of the time at the default `collection_interval` of 5m, so the default stays `10m`, the larger bound for the defaults. The template cannot read the runtime config, so the bounds are written in the template comment and enforced by the e2e: it reads the variable's default, fails if it is below either bound for the defaults, and scales the window by the same factor as both intervals.

Constant over custom/textbox: constants are not shown on the dashboard, change only in the dashboard settings or through provisioning, and are what the chart can set. Chart: `dashboard.minStep` (empty by default). When set, the template parses the generated JSON, sets the `min_step` variable's `query` and `current`, and re-serialises; when empty the ConfigMap stays byte for byte the generated file.

### D3. Inherited self-telemetry metrics as a producer of the product reader

In `newMeterProvider`, when `resolveDestination(SignalMetrics)` is not `fromEnv`, the self-telemetry `MeterProvider` gets a `ManualReader` (with `runtime.NewProducer()`) instead of a `PeriodicReader` + exporter, and `Telemetry.Producer()` returns an `sdkmetric.Producer` wrapping it: `Produce` collects the manual reader and returns its scope metrics. The resource stays on the product push only, so there is one `target_info`. With an env endpoint, `Producer()` returns nil and the separate exporter stays.

Because the product reader is built before telemetry, the product `PeriodicReader` gets a late-binding producer from `exporter.New` (`metric.WithProducer`), and `Provider.AttachProducer(p)` stores `p` once. `main.go` attaches `tel.Producer()` when non-nil.

The wrapper never returns an error: the SDK skips the whole export when any producer fails, and a self-telemetry failure must not block the GitHub metrics. Errors go to `otel.Handle` (logged by the existing error handler) and the wrapper returns nil data. The bridge reader of self-telemetry is unchanged.

Shutdown order stays product first, then telemetry: the final product flush still collects the self-telemetry manual reader.

Alternative: build telemetry first and pass producers into `exporter.New`. Rejected: telemetry needs the bridge registry the provider owns; moving it touches more wiring than a set-once slot.

Product isolation: the product push still goes to `export.otlp` with the same settings; it only carries extra scopes when self-telemetry metrics are active and inherited, which is what "inherit" means. No `OTEL_*` variable changes the product push.

### D4. `target_info` evidence

The e2e runs Prometheus with the stock config (only `otlp.translation_strategy: NoTranslation`, no `out_of_order_time_window`) and self-telemetry metrics inherited through the proxy. It then:

1. reads `target_info` raw samples over the run (`target_info[<run>]`), logs their timestamps, and asserts they are strictly increasing;
1. asserts `prometheus_tsdb_out_of_order_samples_total` on Prometheus' own `/metrics` is 0;
1. asserts `github_exporter.otlp.errors` is 1 (the proxy's planned rejection only).

If all three hold, the README requirement and the e2e window go. If not, both stay and the README states the measured cause.

Measured result: with one writer alone, a stock Prometheus still rejected most pushes with `out of order sample`. The pushes carried `go.schedule.duration` from the contrib runtime producer with the time of its last runtime read, which it repeats for up to its default 15 s minimum read interval; Prometheus writes a resource's `target_info` at the oldest data point time of the push, so each push's `target_info` landed behind the previous one. The self-telemetry runtime producers now read on every `Produce` (`WithMinimumReadMemStatsInterval(time.Nanosecond)`; one `runtime/metrics` histogram read per push). With that, all three checks hold: strictly increasing `target_info` timestamps, 0 out-of-order samples, `otlp.errors` 1. The README requirement and the e2e window are dropped.

### D5. Scrape duration buckets

`1, 5, 10, 30, 60, 120, 300, 600, 900, 1800, 3600` s. Small orgs finish in seconds (1–30 s); the reported 389-repo org takes ~1040 s (600–1800 s range resolves it); a cycle that waits out a GitHub primary rate limit window can last up to the 1 h window (1800, 3600). Sub-second buckets go: the panel sizes `collection_interval`, which is set in minutes, and any faster cycle lands in the 1 s bucket. 11 finite buckets against 9 before: two more series per target.

Exponential histogram rejected: the dashboard and e2e query classic `_bucket` series, and a Prometheus backend needs native-histogram ingestion for OTLP exponential histograms; that is a separate change. The registry note does not list buckets, so it stays.

### D6. `api.errors` initialisation

`SelfMetrics.InitAPIErrors(ctx, target)` adds 0 for every value of `sc.AttrGitHubApiEndpointValues`, called in `Runner.Run` next to `InitOverruns` (the runner's self-metrics interface gains the method). `templates/go/attributes.go.j2` emits, for every enum attribute, `Attr<Id>Values = []string{...}` from the registry members, so the list follows the registry.

`otlp.errors` is not initialised (see proposal): the series that counts push failures is written by `errorCountingExporter` with one `exporter.target` (`cfg.Target`, default `"default"`) on an instrument created on first error; the runner's per-target `RecordOTLPError` only fires on non-export flush failures. Initialising it would need a second mechanism in the exporter wrapper.

## Risks / Trade-offs

- [More OTLP requests: one per minute plus one per cycle] → payload is the same snapshot; operators can raise `export.interval`.
- [Stale data looks fresh: a stuck runner's last snapshot keeps being pushed] → `/healthz` stall detection and `scrape.duration` / `last_over_time` already expose stuck cycles; unchanged.
- \[Bucket boundary change splits `histogram_quantile` across the upgrade\] → quantiles over a window spanning the upgrade are off for one window; acceptable for a 0.x release.
- [Self-telemetry in the product push: a self-telemetry collect failure] → wrapper swallows the error so the product push still goes.
- \[Env endpoint on the same backend as `export.otlp`\] → two writers again; README says so.

## Migration Plan

No config change needed: `export.interval` defaults to 60 s. Dashboards provisioned from the chart or file pick up `$min_step` on upgrade. Rollback: previous image and dashboard.
