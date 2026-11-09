# Design

## Context

See proposal.md for motivation. Today `IssueCollector.countRepo` and `DashboardCollector.findDashboardIssue` each send one `githubv4` query per repo. `githubv4` (via `shurcooL/graphql`) builds the query from a fixed Go struct and, when the response has `errors`, returns them as one error carrying only `message`: the `path` and `type` that tell which alias failed are dropped. Per-repo failures are collected into `FailedRepos()`, and the runner adds one `github_exporter.api.errors` per failed repo.

The `graphql` pool state is already updated from the `X-RateLimit-*` headers of every GraphQL response in `rateLimitTransport`.

## Measurements

Read-only, against `zextras` (395 non-archived repos, about 389 after fork exclusion) with a PAT, using the exporter's selections aliased per repo.

`rateLimit(dryRun: true)` cost and node count:

| Query            | Repos per query | Cost | Node count |
| ---------------- | --------------: | ---: | ---------: |
| issue counts     |               1 |    1 |        101 |
| issue counts     |              50 |    1 |      5,050 |
| issue counts     |             100 |    2 |     10,100 |
| dashboard lookup |               1 |    1 |        100 |
| dashboard lookup |             100 |    1 |     10,000 |
| PR page (open)   |               1 |    2 |      7,100 |
| PR page (open)   |              10 |   20 |     71,000 |
| PR page (open)   |              50 |  101 |    355,000 |

Real execution, wall time and GitHub errors:

| Query            | Repos | Time | Errors                         |
| ---------------- | ----: | ---: | ------------------------------ |
| issue counts     |    10 | 1.6s | none                           |
| issue counts     |    20 | 3.0s | none                           |
| issue counts     |    30 | 5.2s | none                           |
| issue counts     |    40 | 6.4s | none                           |
| issue counts     |    50 | 7.3s | 190 `RESOURCE_LIMITS_EXCEEDED` |
| dashboard lookup |   100 | 2.6s | none                           |
| dashboard lookup |   150 | 4.5s | 150 `RESOURCE_LIMITS_EXCEEDED` |

The issue count query takes about 0.15s per repo on GitHub's side (one `issues { totalCount }` per label), and GitHub returns `RESOURCE_LIMITS_EXCEEDED` on individual aliases and nested fields, with partial data, well before the 10s timeout, the 500,000-node limit or any meaningful point cost.

## Goals / Non-Goals

**Goals:**

- Issue count and dashboard points per cycle proportional to batches, not repos.
- Identical exported series before and after.
- A batch API with no shared mutable state, ready for the worker pool of [#91](https://github.com/gjed/github-activity-exporter/issues/91).

**Non-Goals:**

- Batching PR scans or the title-pattern issue listing.
- Concurrency ([#91](https://github.com/gjed/github-activity-exporter/issues/91)).
- New metrics. The per-batch cost is logged at debug level only.

## Decisions

### Raw GraphQL request on the client

Add `(*Client).QueryRaw(ctx, query string, vars map[string]any) (*RawResponse, error)` in `internal/github`. It POSTs `{query, variables}` through the client's own `http.Client`, so the request goes through the same oauth2, rate-limit pause, 401 hook and otelhttp transports as `githubv4`, and returns `Data map[string]json.RawMessage` and `Errors []GraphQLError{Message, Type, Path}`. A non-200 status or undecodable body is an error.

Alternatives: `reflect.StructOf` with `githubv4` keeps the typed client but still loses `path` and `type`, so a failing alias can only be found by parsing messages; another GraphQL library adds a dependency for one call site.

### Query built from a per-repo selection

A collector-side helper builds `query($o0: String!, $n0: String!, ...) { rateLimit { cost remaining resetAt } r0: repository(owner: $o0, name: $n0) { <selection> } ... }`. The selection is a constant string per collector, equivalent to today's struct tags. The dashboard selection also takes `$filterBy`, the issue selection `$states`, shared by every alias. Alias `rI` and variables `$oI`, `$nI` come from the position `I` in the batch, so names never reach the query text and the alias maps back to `repos[I]` without lookup tables. Each alias's JSON decodes with `encoding/json` into the repository struct the per-repo query already uses (field names match case-insensitively), so per-repo and batched paths share the same result handling.

### Error attribution and fallback

For each GraphQL error, `path[0]` names the alias:

- `RESOURCE_LIMITS_EXCEEDED` on an alias or any nested field: the repo's batched result is not trusted and the repo is retried with the per-repo query. A nested resource-limit error leaves a non-null alias with null counts, so checking for a null alias alone would export wrong values.
- Any other type on an alias (`NOT_FOUND`, `FORBIDDEN`, ...): the repo fails, as its per-repo query would.
- No path, an HTTP error, a timeout or an undecodable body: every repo of the batch is retried with its per-repo query.

A repo whose labels or dashboard candidates have a next page continues with the per-repo query from the batch's `endCursor`, so its first page is not read twice. `FailedRepos()` and the runner's `api.errors` accounting stay as they are.

### Batch size default: 20

Points are not the constraint: 100 repos still cost 1 to 2 points, and 100 repos are 10,100 nodes, 2% of the 500,000 limit. The constraint is GitHub's per-query resource limit on the issue count query: it ran in 6.4s for 40 repos and failed aliases at 50. A default of 20 runs in about 3s, keeps 2x headroom below the first observed failure for orgs with more labels per repo, and costs 20 points per cycle for 389 repos instead of 389. One setting serves both collectors: the dashboard query is cheaper per repo, but 20 already brings it to 20 points per cycle. The upper bound of 100 bounds the query size; a value that hits resource limits degrades to per-repo cost through the fallback rather than to failures.

### Rate-limit state

`QueryRaw` decodes `data.rateLimit` and records `remaining` and `resetAt` on the `graphql` pool through the existing `updateRateLimit`, the function the header path calls, so pause and gauges stay in one place. Header and body report the same window; recording both is idempotent. The batch's `cost` is logged at debug level with the batch size, which is how the per-cycle point sum below is measured.

### Worker-pool readiness

The batch function takes `(ctx, repos)` and returns per-repo results and failures; it reads only immutable collector fields (`states`, `botLogin`, the client). `Collect` loops over batches sequentially and appends results. [#91](https://github.com/gjed/github-activity-exporter/issues/91) can replace the loop with a pool without changing the batch function.

## Expected points per hour

389 repos, 5m interval (12 cycles per hour), costs from the dry-run table. PR scans: an open scan and an event scan of at least one page each, 2 points per page.

| Collector            | Before, per cycle | Before, per hour | After, per cycle | After, per hour |
| -------------------- | ----------------: | ---------------: | ---------------: | --------------: |
| PR scans             |           ≥ 1,556 |         ≥ 18,672 |          ≥ 1,556 |        ≥ 18,672 |
| Issue counts         |               389 |            4,668 |               20 |             240 |
| Dashboard (if on)    |               389 |            4,668 |               20 |             240 |
| Total, dashboard on  |           ≥ 2,334 |         ≥ 28,008 |          ≥ 1,596 |        ≥ 19,152 |
| Total, dashboard off |           ≥ 1,945 |         ≥ 23,340 |          ≥ 1,576 |        ≥ 18,912 |

Issue and dashboard cost drops by 95%, from 9,336 to 480 points per hour. The total stays above both the 5,000 (PAT) and 12,500 (App) budgets: PR scans are 97% of what remains, so this change alone does not make 389 repos at 5m fit. The README's current "389 × 4 × 12 ≈ 18,700" counts the PR scans at 2 points in total; they cost 2 per page. The before/after run against `zextras` replaces these estimates with the measured points (summed `rateLimit.cost` and the `graphql` gauge delta) and goes in the PR description.

## Follow-up: PR scans

Batching PR pages does not save points. GitHub charges a query by the requests its connections need, and a PR page is 1 + 100 × 2 nested connections (`labels(first: 20)`, `reviews(first: 50)`), 2 points per repo whether alone or batched: 10 repos cost 20, 50 cost 101 (table above). Batching would only cut round trips. The lever for PR points is the page shape: reading the review state from `reviewDecision` and `latestReviews(first: N)` instead of `reviews(first: 50)`, a smaller label page, or an open-count-only first query (`pullRequests(states: OPEN) { totalCount }`, batchable at 1 point per batch) when the open-PR gauges need no per-PR attributes. That is a separate issue, not this change.

## Risks / Trade-offs

- [A 502 or timeout now affects up to 20 repos at once] → the whole batch is retried per repo, so a failing batch costs at most 20 extra requests and fails no repo that would have succeeded alone.
- [GitHub's resource limits depend on label counts, not on the setting] → nested and alias-level `RESOURCE_LIMITS_EXCEEDED` sends the repo to the per-repo query; operators can lower `graphql_batch_size`.
- \[The raw request bypasses `githubv4` decoding\] → each alias decodes into the existing per-repo structs, and tests compare batched results with per-repo results on the same fixtures.
- [Tests and fixtures need a raw responder] → the issue and dashboard fixtures implement `QueryRaw` by answering each alias with the data they already serve per repo, so every existing scenario runs through the batch path.

## Migration Plan

No migration. `graphql_batch_size` is optional; existing configs get 20. Rollback is reverting the change; `graphql_batch_size: 1` restores one repo per request without a rollback.
