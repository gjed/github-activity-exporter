# Tasks

## 1. Fixed collection period ([#95](https://github.com/gjed/github-activity-exporter/issues/95))

- [x] 1.1 Declare `github_exporter.scrape.overruns` in `registry/signals.yaml` (counter, `{cycle}`, `exporter.target` required, `annotations.prometheus`); `make generate`; verify `make check-schema` passes and the diff is limited to the new constant in `internal/semconv`
- [x] 1.2 Add `RecordOverrun` to `exporter.SelfMetrics` and `collector.SelfMetricsRecorder`; emit it in the integration live-check conforming metrics; verify `go test ./internal/exporter/...`
- [x] 1.3 Rewrite `Runner.Run` to wait `interval - time.Since(start)` on a stoppable timer, log a warning and call `RecordOverrun` on overrun; verify with `testing/synctest` tests in `internal/collector`: starts spaced by the period, an overrun starts the next cycle at once, cycles never overlap, no catch-up after a long overrun, no overrun recorded on shutdown
- [x] 1.4 README and `config.Health.StallTimeout` doc comment: `collection_interval` is the period between cycle starts, overrun behaviour, stall-timeout derivation; markdown lint on README

## 2. Discovery refresh in production ([#97](https://github.com/gjed/github-activity-exporter/issues/97))

- [ ] 2.1 `Discoverer.Start` ticks only (no initial refresh); add `WithRefreshFailed(func())` called on a failed refresh; update the existing `Start` tests; verify `go test ./internal/discovery/...`
- [ ] 2.2 `buildTargetRunner`: `go disc.Start(ctx)` with the hook wired to `RecordAPIError(target, rest.repos)`; verify `go build ./...`
- [ ] 2.3 `testing/synctest` tests with an in-process GitHub fake: a repo created after startup is collected by the first cycle after `refresh_interval`; a failed refresh keeps the old list, logs and calls the hook; a removed repo stops being collected; startup lists repos once; verify `go test -race ./internal/...`
- [ ] 2.4 README: document per-target `discovery.refresh_interval`; markdown lint on README

## 3. Validation

- [ ] 3.1 `go build ./... && go test -race ./... && make check-schema`; `make generate` leaves no diff; integration live-check (`make test-integration`) passes with the new metric

## Workflow follow-up

- `openspec archive collection-schedule` before the final commit, in the same PR.
- PR body: "Closes #95, closes #97"; assignee gjed.
