# Proposal

## Why

Since [#101](https://github.com/gjed/github-activity-exporter/pull/101) the issue and Dependency Dashboard collectors send one GraphQL query per repo per cycle, and every query costs at least 1 point. For a 389-repo org at the default 5m interval those two collectors alone spend about 9,300 of the `graphql` pool's 5,000 (PAT) or 12,500 (App) points per hour, although the data they read costs a fraction of a point per repo ([#104](https://github.com/gjed/github-activity-exporter/issues/104)).

## What Changes

- The issue count query and the Dependency Dashboard lookup read N repos per request: one aliased GraphQL query (`r0: repository(...) { ... } r1: ...`) per batch instead of one query per repo.
- New per-target setting `graphql_batch_size` (default 20, range 1 to 100) sets N.
- Each batched query also reads `rateLimit { cost remaining resetAt }` and feeds it into the target's `graphql` rate-limit pool state.
- A GraphQL error on some aliases (repo deleted, no access, `NOT_FOUND`) fails only those repos, with `github_exporter.api.errors` counted per failed repo as today. A repo that needs more than one page of labels or dashboard candidates, or that hits a GraphQL resource limit, is finished with the existing per-repo query for that repo only.
- Aliases are generated from the batch position; owner and name are passed as query variables, so repo names never appear in the query text.
- PR scans keep per-repo paging. The issue listing used when `exclude_title_patterns` is set keeps per-repo paging.
- Exported series are unchanged.

## Capabilities

### New Capabilities

- `graphql-batching`: reading several repos per GraphQL request, its batch size setting, per-repo failure isolation, per-repo fallback, and rate-limit state from the query.

### Modified Capabilities

- `issue-collection`: the "Issues only" request count becomes per batch instead of per repo.
- `pr-collection`: the "Renovate dashboard lookup" request count becomes per batch instead of per repo.

## Impact

- `internal/github/client.go`: a raw GraphQL request that returns per-alias data and errors with `path` and `type` (the `githubv4` client drops both).
- `internal/collector/issue.go`, `dashboard.go`, a new batch helper in `internal/collector`, the issue and PR fixtures.
- `internal/config/config.go`: `graphql_batch_size` default and validation.
- `cmd/github-activity-exporter/main.go`: passes the batch size and the raw client to the collectors.
- `README.md`: the configuration example and the GraphQL cost paragraph.
- No metric, attribute or registry change.
