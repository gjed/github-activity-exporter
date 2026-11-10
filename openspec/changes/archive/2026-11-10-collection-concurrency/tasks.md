# Tasks

## 1. Config

- [x] 1.1 Add `collection.concurrency` to `Config` and `Target` (`*int`), resolve each target's value in `Load` (target, global, 4) and validate 1 to 100 with errors naming the setting and target; verify with config unit tests for unset, global only, override, 0 and 101

## 2. Limiter and collectors

- [x] 2.1 Add `collector.Limiter` with `each(ctx, n, fn)`; verify with unit tests that in-flight work never exceeds the limit across concurrent callers and that cancellation starts no new work
- [x] 2.2 Replace `BatchOption` with `CollectorOption` (batch size and limiter), add `WithLimiter`, add `PRCollectorConfig.Limiter`
- [x] 2.3 PR collector: read per-repo inputs before dispatch, run repos through the limiter into slots, merge results, failures, events and windows in repo order
- [x] 2.4 Issue and dashboard collectors: run batches (and title-filter repos) through the limiter into slots, merge in order
- [x] 2.5 Runner: run the three collectors concurrently and wait for all; verify `go test -race ./internal/collector/...` and a test that runs the same multi-cycle fixture sequence (events, failures, removed repos, batches with fallbacks) at concurrency 1 and 8 and compares the exported metrics from a ManualReader
- [x] 2.6 Make the e2e mock GitHub safe for concurrent requests if needed; verify `go test -race ./tests/e2e/...`

## 3. Secondary rate limits

- [x] 3.1 Add the target gate, classification, retry with `GetBody`, exponential fallback and a clock field to `rateLimitTransport`; add `WithSecondaryRateLimitHook`; verify with httptest unit tests: `Retry-After` pauses other pools, 429 without headers waits 60 s then 120 s, plain 403 passes through, retries spent returns the last response with its body, primary 403 with remaining 0 pauses only its pool and retries, cancellation during a pause
- [x] 3.2 Verify under `-race` that concurrent requests all wait on a paused pool and on the target gate, and that concurrent 401s are each reported once

## 4. Self-metric

- [x] 4.1 Declare `github_exporter.api.secondary_rate_limits` in `registry/signals.yaml`, run `make generate` (fix casing in `templates/go/*.j2` if needed), add `RecordSecondaryRateLimit`/`InitSecondaryRateLimits` to `SelfMetrics` and the Runner's recorder interface; verify `make check-schema`, selfmetrics unit tests and the live-checks

## 5. Wiring

- [x] 5.1 In `main.go` build one limiter per target, pass it to the collectors and the hook to the client; verify `go build ./...` and `main_test.go`

## 6. Documentation

- [x] 6.1 README: `collection.concurrency` in the config example, a sizing section (cycle time vs `collection_interval` and `github_exporter.scrape.duration`, points per hour vs the rate-limit gauges, GitHub's secondary limits and the new counter) with the zextras measurements
- [x] 6.2 Helm chart: `examples/large-org-values.yaml` and its entry in the chart README; verify `helm template` renders it and markdown lint on changed `.md` files

## 7. Validation

- [x] 7.1 `go build ./... && go test -race ./... && make check-schema`, `make generate` with no unexpected diff
- [x] 7.2 OTLP and `/metrics` live-checks (`go test -tags integration ./tests/integration/...`) and the dashboard e2e
- [x] 7.3 Real-API runs against `zextras` (read-only, `GITHUB_TOKEN=$(gh auth token)`, dashboard enabled) at concurrency 1, 4 and 8: cycle duration, REST calls and GraphQL points per cycle per pool, 403/429 and secondary-limit counts, and a diff of the exported series of the three runs
- [x] 7.4 If [#90](https://github.com/gjed/github-activity-exporter/issues/90) has merged, rebase and add the secondary rate limit panel to its exporter dashboard (merged during review: rebased, panel "Secondary rate limits per hour" added to the exporter dashboard)
- [x] 7.5 `openspec archive collection-concurrency` before the final commit
