# Design: PR author and lines added/removed

## Context

`PRCollector.scanEvents` (`internal/collector/pr.go`) reads closed and merged PRs by `UPDATED_AT` desc, per repo, through `Limiter.each`, and records `github.pr.close.duration` once per merge or close event through the `eventKey` dedup and the per-repo event window. Events are recorded only after both scans of a repo succeed, serially in repo order, in `recordEvents`. The PR query already reads `author { __typename login }` for the `authors` filter, which converts a GraphQL bot login to its REST form (`renovate` → `renovate[bot]`).

## Goals / Non-Goals

**Goals:** the size histograms and the optional author attribute, with no new request and no new dedup path.

**Non-Goals:** filtering by author beyond the existing filter ([#24](https://github.com/gjed/github-activity-exporter/issues/24)); author on counters or gauges; normalising bot logins.

## Decisions

### Record next to `github.pr.close.duration`

The size values ride on the same event that records the close duration: the merged or closed branch of `scanEvents`, gated by the same `count()` dedup. A PR is recorded once per merge or close event, across cycles and at any concurrency, with no new state. Each per-repo worker fills its own `prEvents`; recording stays in the serial merge loop. The branch is chosen by the PR's state at scan time, so a PR closed, reopened and merged inside one window is recorded once, as merged. A PR closed in one cycle, then reopened and merged in a later cycle, is recorded once per cycle, exactly as `github.pr.close.duration` is today.

### GraphQL query cost: 0 points

`additions` and `deletions` are scalar fields of `PullRequest`; the GraphQL point cost counts connections, not scalars. Measured on 2026-11-14 against `zextras/carbonio-mails-ui`, the event-scan query (100 closed/merged PRs, `labels(first: 20)`, `reviews(first: 50)`, `rateLimit { cost }`):

| Query                   | `rateLimit.cost` per page |
| ----------------------- | ------------------------- |
| current fields          | 2                         |
| + `additions deletions` | 2                         |

`author { login }` is already fetched. The open scan shares the query struct and also receives the two integers; splitting the struct to save a few bytes per node is not worth the second shape. Re-measured on full cycles against the 392 zextras repos (PAT, concurrency 8): a cycle with the open and event scans costs 849 points before and 840 after, within the noise of PRs updated between runs.

### `github.pr.author` value: the GraphQL login, verbatim

The attribute is `author.login` as GraphQL returns it: a GitHub App bot is `renovate`, not the REST `renovate[bot]`. The `authors` filter keeps matching the REST form, so `authors: ["renovate[bot]"]` selects the PR and its series carries `github.pr.author="renovate"`. A PR whose author account was deleted (GraphQL `author: null`) is recorded without the attribute. The README documents both.

### Setting: `collection.pr_author`

Same pattern as `collection.concurrency`: top-level `collection.pr_author` (bool, default `false`) is the default of every target, `targets[].collection.pr_author` overrides it, `Load` resolves the target value. A non-boolean value fails `Load` with an error naming the key. The env override `GITHUB_ACTIVITY_EXPORTER_COLLECTION_PR_AUTHOR` works through the viper default.

### Bucket boundaries

`0, 10, 50, 100, 250, 500, 1000, 2500, 5000, 10000` (10 boundaries, 11 buckets with `+Inf`), shared by both histograms. Sample: 200 zextras PRs closed since 2026-10-15:

|           | p50 | p75 | p90 | p99    | max     |
| --------- | --- | --- | --- | ------ | ------- |
| additions | 5   | 43  | 789 | 15,587 | 17,789  |
| deletions | 1   |     | 191 | 2,708  | 124,552 |

- `0` isolates pure deletions (additions) and pure additions (deletions).
- `10 … 1000` follow the usual PR size classes (XS < 10, S < 50, M < 250, L < 1000) where p50 to p90 sit, so `histogram_quantile` interpolates inside a class.
- `2500 … 10000` separate large refactors and generated or vendored changes from the tail; anything larger lands in `+Inf`.

### `github.pr.close.duration` gets the author under the same flag

The issue asks whether bot PRs are "small and fast"; size without close time per author answers half of it. With the flag off the metric is unchanged; with it on, each repo series splits per author. One flag, one cardinality decision.

### Series cost on zextras

Estimate from the REST search API over PRs closed 2026-10-15 to 2026-11-14 (30 days, the lookback): 2,666 PRs, 177 repos with at least one closed PR, 40 authors, 524 distinct author × repo pairs (`ZxBot` alone authored 1,369 PRs). A classic histogram is buckets + 2 series per attribute set (`_bucket` × 11, `_sum`, `_count`) = 13 for the size histograms, 12 for `github.pr.close.duration` (9 boundaries). The bound (buckets + 2) × authors × repos × 2 is 13 × 40 × 177 × 2 = 184,080 for the size histograms; real pairs are 7 % of the cross product.

Measured on 2026-11-14 by running the PR collector once over the 392 zextras repos with a 30-day event window (2,559 merged or closed PRs):

|                                      | flag off | flag on |
| ------------------------------------ | -------- | ------- |
| attribute sets per histogram         | 169      | 511     |
| lines added + removed: 2 × 13 × sets | 4,394    | 13,286  |
| close.duration: 12 × sets            | 2,028    | 6,132   |
| total                                | 6,422    | 19,418  |

The SDK keeps cumulative state for every attribute set since process start, so series grow with the pairs seen since the last restart, not with a sliding 30 days: a long-lived pod over a busy org exceeds the table. The SDK (v1.47) caps each instrument at 2,000 attribute sets by default; beyond that, new sets are merged into one `otel.metric.overflow="true"` set, so per-author data is lost rather than memory growing without bound. zextras is at 511 sets per instrument.

### KPI dashboard

A "PR size" panel pair in the Pull requests row: `histogram_quantile` p50 and p90 of each histogram by `github.org`, `github.repo`. One per-author panel: p90 lines added and removed by `github.pr.author`, with the matcher `"github.pr.author"!=""` so the panel has no series when the setting is off, instead of one unlabeled series. All built through `m.expr` from `annotations.prometheus`.

## Risks / Trade-offs

- [Unbounded author cardinality on large orgs] → off by default, per-target opt-in, series table and warning in both READMEs.
- [Metric login differs from filter login for bots] → documented in the README next to the `authors` filter.
- \[`additions`/`deletions` on huge PRs\] → GitHub returns the stored totals; values above 10,000 fall into `+Inf`.

## Migration Plan

Additive. Upgrading adds two histograms; nothing changes until a target sets `collection.pr_author: true`. Rollback: unset it or downgrade.
