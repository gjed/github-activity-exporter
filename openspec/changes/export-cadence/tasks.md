# Tasks

## 1. Export cadence (#89)

- [ ] 1.1 Add `export.interval` to `internal/config` (default `60s`, fatal when not positive, error names the key); verify with config unit tests for default, override and `0s`/negative.
- [ ] 1.2 Rename `exporter.Config.CollectionInterval` to `ExportInterval`, feed it to the product `PeriodicReader`, pass `cfg.CollectionInterval` to the runners in `main.go`; verify `go build ./...` and the existing exporter tests.
- [ ] 1.3 Unit test: one counter `Add` and one histogram `Record`, then several periodic pushes and flushes into an in-memory exporter, assert the same cumulative sum, count and start time on every push and that pushes happen on `ExportInterval` without a flush.
- [ ] 1.4 Dashboard template: `min_step` constant variable (default `2m`), every panel `"interval": "$min_step"`, comments updated (gauge `last_over_time` rationale now `export.interval`); run `make generate` and verify the dashboard and chart copy diff only in those fields.
- [ ] 1.5 Chart: `dashboard.minStep` in `values.yaml` and `templates/dashboard.yaml`, chart README; verify `helm template` with and without the value (byte-for-byte when empty, only `min_step` changed when set).
- [ ] 1.6 README: `export.interval` in the config reference and example, push cadence explanation, dashboard `$min_step`; run markdown lint on the changed files.
- [ ] 1.7 e2e: `collection_interval: 15m`, short `export.interval` scaled from the default, `$min_step` read from the dashboard and checked against 2 × default `export.interval`; a range query of every `rate()` panel over the run has a value at every step; `github_exporter.scrape.duration_count` stays 1 across pushes.

## 2. One target_info writer (#99)

- [ ] 2.1 `internal/exporter`: late-binding producer on the product reader and `Provider.AttachProducer`; unit test that attached data appears in the next push and that a producer error does not block the push.
- [ ] 2.2 `internal/telemetry`: inherited case uses a `ManualReader` (with the runtime producer) exposed by `Telemetry.Producer()`; env-endpoint case unchanged with `Producer()` nil; `main.go` attaches it. Unit tests for both cases, including `OTEL_METRIC_EXPORT_INTERVAL` ignored when inherited.
- [ ] 2.3 e2e: self-telemetry inherited through the proxy, stock Prometheus config (no `out_of_order_time_window`); log `target_info` sample timestamps and assert strictly increasing, `prometheus_tsdb_out_of_order_samples_total` 0, `github_exporter.otlp.errors` 1.
- [ ] 2.4 README self-telemetry section and `OTEL_METRIC_EXPORT_INTERVAL` row: when it applies; drop or keep the out-of-order backend requirement per 2.3 evidence; note that an env endpoint on the same backend brings two writers back. Update `charts/.../examples/self-telemetry-values.yaml` comment. Markdown lint.

## 3. Scrape duration buckets (#94)

- [x] 3.1 `selfmetrics.go` boundaries `1, 5, 10, 30, 60, 120, 300, 600, 900, 1800, 3600`; unit test that a 1040 s observation lands in the 1800 bucket.

## 4. api.errors initialisation (#103)

- [x] 4.1 `templates/go/attributes.go.j2`: per-enum `Attr<Id>Values` list; `make generate`; verify `AttrGitHubApiEndpointValues` holds `graphql` and `rest.repos`.
- [x] 4.2 `SelfMetrics.InitAPIErrors` over `sc.AttrGitHubApiEndpointValues`, called in `Runner.Run` next to `InitOverruns`; unit test that a started runner exports 0 per endpoint value before any error.

## 5. Integration

- [ ] 5.1 `go build ./...`, `go test ./...`, `make check-schema`, `make generate` with no diff, OTLP and `/metrics` live-checks, dashboard e2e (`make test-e2e`) with self-telemetry metrics on; paste output.

## Workflow follow-up

- `openspec archive export-cadence` before the final commit, in this PR.
- PR body: "Closes #89, closes #99, closes #94, closes #103"; assignee gjed; push only after diff approval.
