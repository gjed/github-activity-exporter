# Design

## Context

See proposal.md for motivation. Today `PRCollector.fetchPRs` (`internal/collector/pr.go`) runs one GraphQL query, `pullRequests(states: [OPEN, CLOSED, MERGED])` in default (creation) order, and stops at `defaultMaxPRsPerRepo = 500`. One list feeds every PR metric. `Runner` (`internal/collector/runner.go`) already keeps `lastCollectedAt` per repo (start of the last successful cycle) and gates `close.duration`/`automerged` events with it; the first cycle records no events. `filter.PRFilter` matches state and labels only. `main.go` always builds the dashboard collector.

GraphQL `Repository.pullRequests` accepts `states`, `labels` (any-of), `headRefName` (exact match), `baseRefName` and `orderBy` (`CREATED_AT`/`UPDATED_AT`). It has no author or branch-prefix argument.

## Goals / Non-Goals

**Goals:**

- Exact created/merged/closed rates per repo for a filtered PR population, with no truncation.
- Open-state gauges over every open PR.
- One event scan that later work ([#53](https://github.com/gjed/github-activity-exporter/issues/53)) can consume.

**Non-Goals:**

- [#53](https://github.com/gjed/github-activity-exporter/issues/53) and any new histogram.
- Label-derived attributes (#24 part 2).
- Exact all-time closed/merged totals.
- Events that happen while the exporter is down (window state stays in memory).
- Changing `github.pr.label` on `github.pr.count`.

## Decisions

### 1. Two queries per repo, one query shape

One GraphQL query type with variables `$states`, `$labels`, `$orderBy`, `$cursor`:

- **Open scan**: `states: [OPEN]`, `orderBy: {CREATED_AT, ASC}`, paginated to the end. Feeds `github.pr.count` (open, draft), `github.pr.age`, `github.pr.review_status`.
- **Event scan**: `states` from the filter (all when empty), `orderBy: {UPDATED_AT, DESC}`. Stops after the first page whose last node has `updatedAt` before the window start. Skipped on a repo's first cycle. Feeds `github.pr.created`, `github.pr.merged`, `github.pr.closed`, `github.pr.close.duration`, `github.pr.automerged`.

Any event changes `updatedAt` to at least the event time, so every PR with an event in the window has `updatedAt` past the window start and sits before the stop point.

Alternative: GraphQL `totalCount` per state for totals (#26 proposal). Rejected: `totalCount` honours `labels`/`states` only, so with `authors`, `head_branch_prefix` or `exclude_labels` set it counts the wrong population. Alternative: Search API (`author:`, `head:`). Rejected: 1000-result cap per query, a separate 30 req/min rate limit, and no prefix match on `head:`.

### 2. Event window `(start, end]`, recorded only on success

`end` is the cycle start time the Runner already stores as the next `lastCollectedAt`; `start` is `max(end − 30d, lastCollectedAt)`. An event at time `t` is recorded iff `start < t ≤ end`. Consecutive windows of a repo share only their boundary, which belongs to exactly one, so no event is counted twice and none falls between. `Collect` takes `end` from the Runner instead of calling `time.Now()`.

Events are gathered in memory and recorded only after both scans of a repo succeed. A failed scan records nothing and keeps the old `lastCollectedAt`, so the next cycle covers the whole gap once. Today a failure after partial recording would double count on retry.

Within one event scan, PRs are deduplicated by number: a PR updated mid-scan can reappear on a later page.

Event definitions: created = `createdAt` in window; merged = `mergedAt` in window; closed = state `CLOSED` and `closedAt` in window (merged PRs are not closed, matching the disjoint `github.pr.state` values). `close.duration` and `automerged` keep their current definitions on the same window.

### 3. Counter attributes include `exporter.target`

The new counters carry `exporter.target`, `github.org`, `github.repo`, like every other PR metric. Two targets may watch the same repo with different filters (for example a Renovate target and an all-PRs target); without `exporter.target` their series would collide. Global rate: `sum(rate({"github.pr.merged"}[1h]))`; per repo: `sum by ("github.org", "github.repo") (...)`.

### 4. Series exist from the first cycle

On a repo's first successful cycle each new counter gets `Add(0)`. Prometheus `increase()`/`rate()` ignore the first sample of a series, so without it the first event of a quiet repo would be lost. Cost: three series per repo per target, bounded by discovery.

### 5. `github.pr.count` keeps only `open` and `draft`

Closed/merged totals need either full-history listing (unbounded cost, the #26 bug) or `totalCount` (wrong under client-side filters, Decision 1). Open and draft counts come exact from the open scan. Closed/merged volumes are answered by `increase({"github.pr.merged"}[30d])` etc. The `github.pr.state` attribute keeps all four values: `states` filters still use them.

### 6. Filters: server-side where GraphQL can, client-side always

- `include_labels` → `$labels` (GraphQL any-of matches the current any-of semantics).
- `states` → `$states` on the event scan (`draft` maps to `OPEN`). The open scan always uses `[OPEN]`, and is skipped when `states` excludes both `open` and `draft`.
- `authors`, `head_branch_prefix`, `exclude_labels`: client-side only.

`filter.PRFilter.Match` re-checks every filter client-side, so server-side arguments only cut traffic and never change the result. The node gains `updatedAt`, `headRefName` and `author { __typename login }`. GraphQL returns a GitHub App bot's login without `[bot]` (`renovate`); for `__typename: Bot` the collector appends `[bot]` so config uses the REST login (`renovate[bot]`), as in the existing `RENOVATE_BOT_LOGIN`. `head_branch_prefix` is a single string, case-sensitive like git refs.

### 7. No label-derived attributes (#24 part 2 dropped)

The Weaver registry is compiled into the binary: attribute keys and enum values must be declared at build time to pass live-check and to be generated into `internal/semconv`. A per-target, config-defined label→attribute mapping produces keys and values only known at runtime, which either break the contract or require every user's mapping in the upstream registry. The same grouping is available today with one target per group (for example `include_labels: [major]`), distinguished by `exporter.target`. Renovate-specific metadata stays on the dashboard collector (`renovate.*`).

### 8. Dashboard collector opt-in per target

New target block `renovate_dashboard: { enabled: false }`. When false, `main.go` passes a nil `Dashboard` collector to the Runner, which already skips nil collectors. The bot login stays `RENOVATE_BOT_LOGIN`. The README gains a "Renovate preset": `filters.prs.authors: [renovate[bot]]` (or `include_labels: [renovate]`) plus `renovate_dashboard.enabled: true`. The README config block is the example config; the repo has no separate example file.

## Risks / Trade-offs

- [A PR updated again while the event scan pages past it moves to page 1 and can be skipped, losing its event] → Rare (needs an update between two page fetches of a short scan); accepted. Dedup handles the opposite case (seen twice).
- [Clock skew between exporter and GitHub shifts window edges] → Both edges move together; an event is still counted once. A skew larger than the cycle can delay an event by one cycle.
- [A repo with thousands of open PRs costs many pages each cycle] → Exact by requirement; cost is linear in open PRs. Renovate repos stay small via `prConcurrentLimit`.
- \[`github.pr.count` closed/merged series disappear\] → Documented in README; queries move to the counters.
- [Counters reset on restart; events while down are lost] → `rate()`/`increase()` handle resets; downtime gaps are accepted (unchanged from today).
- \[Server-side `labels` matching differs from client-side case-insensitive matching\] → GitHub label names are unique case-insensitively per repo; the client-side re-check keeps exclusion exact.

## Migration Plan

- Targets that need dashboard metrics add `renovate_dashboard.enabled: true`.
- Replace `{"github.pr.count", "github.pr.state"=~"closed|merged"}` queries with `increase({"github.pr.closed"}[...])` / `increase({"github.pr.merged"}[...])`.
- Rollback: deploy the previous image; no persisted state.
