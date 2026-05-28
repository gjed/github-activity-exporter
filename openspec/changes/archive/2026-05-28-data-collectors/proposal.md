## Why

This change implements the core business value of the exporter: collecting PR and issue state from GitHub and recording it as metrics. It answers the primary question — "is Renovate actually helping?" — by tracking PR age, merge rates, automerge events, closed-without-merge rates, and Renovate Dependency Dashboard queue state. It depends on `github-client` (authenticated API client + repo list) and `observability-contract` (metric names and OTel MeterProvider).

## What Changes

- `internal/collector/pr.go`: PR metrics collector — fetches PR state via GraphQL, records age, state breakdown, automerge, label grouping, review status
- `internal/collector/issue.go`: Issue metrics collector — fetches issues via REST, records counts and age
- `internal/collector/dashboard.go`: Dependency Dashboard parser — identifies Renovate's dashboard issue, parses queue state sections, records queue metrics
- `internal/filter/`: PR and issue filter engine — label include/exclude, title pattern, state filter; configurable per target
- Collection loop orchestration: one goroutine per target, drives all collectors on each cycle

## Capabilities

### New Capabilities

- `pr-metrics`: PR age, state (open/merged/closed-without-merge), automerge detection, label grouping, review status
- `issue-metrics`: Issue counts by state and label, issue age
- `issue-pr-filtering`: Label include/exclude, title pattern exclusion, state filter; per-target
- `dashboard-parser`: Dependency Dashboard identification, section parsing (awaiting/rate-limited/pending/open), queue metrics, parse error metric

### Modified Capabilities

## Impact

- New packages: `internal/collector/`, `internal/filter/`
- Depends on: `observability-contract` (for `internal/semconv/` constants and `MeterProvider`), `github-client` (for `internal/github/client.go` and `internal/discovery/`)
- Parallel with: nothing — this is the final layer; both `observability-contract` and `github-client` must be complete first
