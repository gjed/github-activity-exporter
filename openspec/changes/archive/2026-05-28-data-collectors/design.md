## Context

The collectors are the revenue layer of the exporter. They use the GitHub client from `github-client`, record metrics via the OTel `Meter` from `observability-contract`, and apply filters configured per target. The collection loop runs each collector in sequence for each target on every cycle.

## Goals / Non-Goals

**Goals:**

- PR GraphQL query fetches all needed fields in one paginated request
- In-memory cache per target per repo: skip re-fetching data unchanged since last cycle
- Filters applied before metric recording (not after) to avoid unnecessary work
- Dependency Dashboard parsed via deterministic Markdown section matching
- All metric names from `internal/semconv/` constants — zero magic strings

**Non-Goals:**

- Per-PR-number label cardinality — metrics are aggregated by repo/state/label, not per PR number
- Historical backfill — only current state; time series accumulation is Prometheus/Grafana's job
- Dependency Dashboard writing or editing

## Decisions

### Decision: GraphQL v4 for PR queries, REST for issues

Rationale: PRs need many fields simultaneously (state, labels, createdAt, mergedAt, closedAt, reviews) — GraphQL fetches all in one page. Issues only need number, title, state, labels, createdAt — REST is simpler and sufficient.

### Decision: Aggregated metrics only (no per-PR-number labels)

Rationale: Per-PR label cardinality would explode for orgs with hundreds of repos and thousands of PRs. All metrics are aggregated: `github.pr.count{repo, state}`, age is a gauge of the oldest open PR per repo (not per PR). Histogram `github.pr.close.duration` bins close ages across all PRs — no per-PR labels.
If per-PR drill-down is needed, that's a Grafana explore query against raw GitHub data, not a metric.

### Decision: PR age gauge = age of oldest open PR per repo

Rationale: The single most actionable signal — "how stale is the oldest unreviewed Renovate PR in this repo?" Rather than a distribution of all open PR ages (which is the histogram's job), the gauge gives an at-a-glance worst-case signal per repo.

### Decision: Automerge detection via "merged with no approved review"

Rationale: GitHub doesn't expose an explicit "automerge" flag reliably across all merge methods. A PR merged with no `APPROVED` review decision is a reliable proxy for automerge (Renovate-configured automerge doesn't wait for reviews). Edge case: repos with no branch protection also pass this test — acceptable for MVP, document the limitation.

### Decision: Dashboard parser uses deterministic section header regex

Rationale: Renovate's Dependency Dashboard format is consistent: `## <section>` headers followed by checkbox lists. Regex `^## (Awaiting Schedule|Rate-Limited|Pending Approval|Open)\s*$` captures section boundaries. Count `- [x]` and `- [ ]` list items within each section. If no sections are found, emit `renovate.dashboard.parse_error = 1`.

### Decision: Filter applied at collection time, before metric recording

Rationale: More efficient — don't fetch data for repos/PRs you'll immediately discard. Glob and label filters applied during result iteration, not as a post-processing step.

## Risks / Trade-offs

- **Dependency Dashboard format change** → Mitigation: `renovate.dashboard.parse_error` metric + alert on it in generated dashboard; note Renovate version sensitivity in docs
- **Large PR sets (>1000 per repo)** → Mitigation: configurable `max_prs_per_repo` limit; log warning when truncated; default 500
- **GraphQL query complexity limits** → Mitigation: request 100 PRs per page (safe for complexity budget); paginate with cursor

## Open Questions

- Should `github.pr.age` be the oldest open PR or a histogram of all open PR ages? → Oldest open PR as gauge; all open PR ages as histogram (both, different questions)
- For closed-without-merge: look back how far? → Default last 30 days to avoid unbounded queries; configurable `lookback_days`
