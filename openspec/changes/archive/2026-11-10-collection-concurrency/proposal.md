# Proposal

## Why

A collection cycle sends its GitHub requests one at a time ([#91](https://github.com/gjed/github-activity-exporter/issues/91)). `Runner.collect` runs the PR, issue and Dependency Dashboard collectors one after another; the PR collector pages each repo in turn, and the issue and dashboard collectors run their batches in turn. Cycle time is request latency times request count, not rate-limit budget. Since [#95](https://github.com/gjed/github-activity-exporter/issues/95) a cycle longer than `collection_interval` is an overrun, so on a large org the configured freshness is out of reach while most of the budget stays unused. Measurements on current main against `zextras` are in design.md.

## What Changes

- New setting `collection.concurrency`: a global default at the top level of the config file and a per-target override, range 1 to 100, default 4. `1` sends one request at a time, as today.
- A per-target worker pool runs at most `concurrency` units of work at once. A unit is one repo of the PR collector (open scan and event scan), one batch of the issue or dashboard collector (with its per-repo fallbacks), or one repo of the issue title-filter listing. Each unit sends its requests one after another, so a target has at most `concurrency` collection requests in flight.
- The three collectors of a cycle run at the same time and share the target's pool.
- Exported series are identical for every `concurrency` value: per-repo results are merged in repo order after the workers finish, and the PR event window, `lastCollectedAt`, failed-repo lists, the snapshot and the per-cycle flush keep their current semantics.
- Secondary rate limits: a 403 or 429 response that is a rate limit pauses every request of the target until the time GitHub gives (`Retry-After`, or `X-RateLimit-Reset` when `X-RateLimit-Remaining` is 0, or 60 s with exponential back-off when neither is set), then the request is retried, up to 3 times. A primary-limit exhaustion keeps pausing only its pool, as today.
- New self-metric `github_exporter.api.secondary_rate_limits` (counter, per target) counts the secondary rate limit responses. A "Secondary rate limits per hour" panel on the exporter dashboard of [#90](https://github.com/gjed/github-activity-exporter/issues/90), which merged during review.
- README: how to size `concurrency` against `collection_interval`, `github_exporter.scrape.duration` and the rate-limit gauges. Helm chart: a values example for a large org.

## Capabilities

### New Capabilities

- `collection-concurrency`: the `collection.concurrency` setting, the per-target worker pool, concurrent collectors, and identical output for any concurrency.

### Modified Capabilities

- `github-auth`: secondary rate limit handling (target-wide pause and retry) and the primary pool pause and 401 reporting with concurrent callers.
- `metric-export`: the `github_exporter.api.secondary_rate_limits` self-metric.

## Impact

- `internal/config/config.go`: `collection.concurrency` global and per target, default and validation.
- `internal/collector`: a shared per-target limiter; `runner.go` runs the collectors concurrently; `pr.go`, `issue.go`, `dashboard.go` dispatch repos and batches to it and merge results in repo order.
- `internal/github/client.go`: secondary rate limit detection, target-wide pause, retry and a hook.
- `registry/signals.yaml`, `internal/semconv/*.go` (generated), `internal/exporter/selfmetrics.go`: the new counter.
- `cmd/github-activity-exporter/main.go`: wiring.
- `README.md`, `charts/github-activity-exporter/examples/`, chart README.
- No change to existing metric names, attributes or values.
