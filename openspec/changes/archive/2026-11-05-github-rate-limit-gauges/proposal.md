# Proposal

## Why

The `github-auth` spec requires `github_exporter.api.rate_limit.remaining` and `github_exporter.api.rate_limit.reset` per target, but nothing emits them ([#16](https://github.com/gjed/github-activity-exporter/issues/16)). The GitHub client already tracks the rate-limit state and exposes `WithRateLimitHook`, but nothing registers the hook. When a target hits its limit, collection pauses and the only trace is a log line.

## What Changes

- Registry (`registry/signals.yaml`): two gauges, pure OTel names per the naming contract ([#14](https://github.com/gjed/github-activity-exporter/issues/14)), each with `exporter.target` required:
  - `github_exporter.api.rate_limit.remaining`, unit `{request}`: requests left in the current window.
  - `github_exporter.api.rate_limit.reset`, unit `s`: Unix time, in seconds, at which the window resets.
- Templates: `replace("Ratelimit", "RateLimit")` in `templates/go/metrics.go.j2`, so the constants are `MetricGitHubExporterApiRateLimitRemaining` and `MetricGitHubExporterApiRateLimitReset`. `make generate` regenerates `internal/semconv/metrics.go` and adds two panels to the generated `dashboards/github-activity.json` (the template renders every `github_exporter.*` metric).
- Self-metrics (`internal/exporter/selfmetrics.go`): the two gauges plus a method that records both for a target.
- Wiring (`cmd/github-activity-exporter/main.go`): each target's GitHub client registers `ghclient.WithRateLimitHook`, which records the gauges with `exporter.target` = the target name.
- Tests: the live-check integration test emits both gauges with their attributes and checks them against the registry.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `github-auth`: the "Rate limit metrics exposed" scenario states when the gauges are recorded, their units and the `exporter.target` attribute.
- `weaver-schema`: the list of registry metrics includes the two gauges.

## Impact

- Code: `internal/exporter/selfmetrics.go`, `cmd/github-activity-exporter/main.go`, `tests/integration/live_check_test.go`.
- Registry/templates: `registry/signals.yaml`, `templates/go/metrics.go.j2`; regenerated `internal/semconv/metrics.go` and `dashboards/github-activity.json`.
- Specs: `openspec/specs/github-auth`, `openspec/specs/weaver-schema`.
