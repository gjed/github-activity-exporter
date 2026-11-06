# Proposal

## Why

The exporter is a general GitHub PR/issue activity exporter, but its PR metrics cannot be scoped to a PR population (only labels and states filter), and the PR collector lists full history newest-created first and stops at 500 PRs per repo, so totals on large repos are silently truncated and recent close/merge events of older PRs are missed. The MVP goal is to see how fast PRs are created and merged, globally and per repo, for a filtered population such as Renovate PRs.

Resolves [#26](https://github.com/gjed/github-activity-exporter/issues/26) and [#24](https://github.com/gjed/github-activity-exporter/issues/24) (PR selection filters only; label-derived attributes are dropped, see design.md).

## What Changes

- New per-target PR selection filters `filters.prs.authors` and `filters.prs.head_branch_prefix`, alongside the existing `include_labels`, `exclude_labels` and `states`. Include labels and states are applied in the GraphQL query; authors, branch prefix and exclude labels are applied client-side.
- New counters `github.pr.created`, `github.pr.merged` and `github.pr.closed`, one increment per PR event since the previous successful cycle, with `exporter.target`, `github.org` and `github.repo`.
- Event scan: each cycle reads PRs ordered by `UPDATED_AT` desc and stops at `max(lookback cutoff, last collection time)`. It is the single source of closed/merged events for `github.pr.close.duration`, `github.pr.automerged` and the new counters.
- Open-PR scan: all open PRs are paginated with no cap; `github.pr.count`, `github.pr.age` and `github.pr.review_status` come from it.
- Remove the 500-PR cap (`defaultMaxPRsPerRepo`, `PRCollectorConfig.MaxPRsPerRepo`) and the full-history listing. `github.pr.count` reports only `open` and `draft`; exact all-time closed/merged totals are no longer exported.
- The Renovate Dependency Dashboard collector is opt-in per target (`renovate_dashboard.enabled`, default `false`). The README documents a "Renovate preset".

## Capabilities

### New Capabilities

- `pr-collection`: which PRs a target selects, which PR state gauges and event counters are exported, and the event window that makes counters exact across cycles.

### Modified Capabilities

- `weaver-schema`: the registry metric list adds `github.pr.created`, `github.pr.merged` and `github.pr.closed`.

## Impact

- Code: `internal/collector/pr.go`, `internal/collector/pr_fixture.go`, `internal/collector/runner.go`, `internal/filter/pr.go`, `internal/config/config.go`, `cmd/github-activity-exporter/main.go`.
- Registry: `registry/signals.yaml` (three new counters, `github.pr.count` brief/note); regenerated `internal/semconv/*.go` and `dashboards/github-activity.json`.
- Config: two new filter keys and a new `renovate_dashboard` block per target. Targets that relied on the dashboard collector must set `renovate_dashboard.enabled: true`.
- Metrics: `github.pr.count` series with `github.pr.state` `closed` or `merged` disappear. Dashboards and alerts on them move to `increase({"github.pr.merged"}[...])` / `increase({"github.pr.closed"}[...])`.
- GitHub API usage: per repo per cycle, one paginated open-PR query plus an event query bounded by time instead of up to 5 pages of full history.
- Overlap: the parallel rate-limit-per-pool change touches `registry/signals.yaml`; rebase on `origin/main` if it merges first.
