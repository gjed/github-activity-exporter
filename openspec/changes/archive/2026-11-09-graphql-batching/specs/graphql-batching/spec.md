## Purpose

Defines how the exporter reads several repositories in one GraphQL request for issue counts and the Dependency Dashboard lookup, and how a batch fails, falls back and reports rate-limit state per repo.

## ADDED Requirements

### Requirement: Batched repo queries

Issue count collection and the Dependency Dashboard lookup SHALL read up to `graphql_batch_size` repos of a target per GraphQL request, in discovery order. PR scans and the issue listing used with `exclude_title_patterns` SHALL keep one request per repo.

#### Scenario: Repos split into batches

- **WHEN** a target has 45 repos, `graphql_batch_size: 20`, and every repo fits in one page
- **THEN** issue count collection makes 3 GraphQL requests, for 20, 20 and 5 repos, and the dashboard lookup makes 3 more

#### Scenario: Same values as per-repo queries

- **WHEN** the collectors run with any `graphql_batch_size`
- **THEN** every exported `github.issue.*` and `renovate.dashboard.*` series has the value it has with `graphql_batch_size: 1`

### Requirement: Batch size setting

A target SHALL accept `graphql_batch_size`, an integer from 1 to 100, defaulting to 20 when unset. Any other value SHALL fail configuration loading with an error naming the target.

#### Scenario: Default

- **WHEN** a target does not set `graphql_batch_size`
- **THEN** batches hold at most 20 repos

#### Scenario: Out of range

- **WHEN** a target sets `graphql_batch_size: 0` or `graphql_batch_size: 101`
- **THEN** the exporter fails at startup with an error naming the target and the setting

### Requirement: Per-repo failure isolation

A GraphQL error attributed to one repo of a batch SHALL fail only that repo. The other repos of the batch SHALL be collected from the same response. Each failed repo SHALL be counted once in `github_exporter.api.errors`, as for a per-repo query.

#### Scenario: Deleted repo in a batch

- **WHEN** one repo of a 20-repo batch was deleted after discovery and GitHub answers `NOT_FOUND` for it
- **THEN** that repo is reported as failed, `github_exporter.api.errors` grows by 1, and the other 19 repos export their series

### Requirement: Per-repo fallback

A repo SHALL be finished with per-repo requests when its batched result is incomplete: more than one page of labels or dashboard candidates, or a GraphQL resource-limit error for that repo. A batch request that fails as a whole SHALL be retried as per-repo requests for its repos. Only repos failing their per-repo request SHALL count as failed.

#### Scenario: Repo with more than 100 labels

- **WHEN** one repo of a batch has 150 labels
- **THEN** its remaining labels are read with per-repo requests continuing from the batch's page, and the other repos of the batch need no further request

#### Scenario: Whole batch rejected

- **WHEN** GitHub answers a batch request with an HTTP 502 or a GraphQL error not attributed to a repo
- **THEN** each repo of the batch is collected with its per-repo request, and only repos whose per-repo request fails are reported as failed

### Requirement: Repo identity in a batch

Each repo of a batch SHALL be addressed by an alias generated from its position in the batch, with its owner and name passed as query variables. Repo names SHALL NOT appear in the query text.

#### Scenario: Name that is not a valid alias

- **WHEN** a repo is named `.github` or `my-repo.v2`
- **THEN** it is collected in a batch and its series carry `github.repo` set to that name

### Requirement: Rate-limit state from batched queries

Each batched query SHALL read `rateLimit { cost remaining resetAt }`. When the response carries no `X-RateLimit-Remaining` header, `remaining` and `resetAt` SHALL be recorded as the target's `graphql` pool state, the state that pauses requests and drives `github_exporter.api.rate_limit.*`; otherwise the header values, recorded for every GraphQL response, are the pool state.

#### Scenario: Pool state after a batch without rate-limit headers

- **WHEN** a batched query returns `rateLimit { remaining: 150 }` and no `X-RateLimit-*` headers
- **THEN** the target's `graphql` pool is paused until `resetAt` and `github_exporter.api.rate_limit.remaining{github.api.rate_limit.resource="graphql"}` reports 150

#### Scenario: Pool state after a batch with rate-limit headers

- **WHEN** a batched query returns `X-RateLimit-Remaining: 4000` and `rateLimit { remaining: 4000 }`
- **THEN** the `graphql` pool state is recorded once, from the headers

### Requirement: Batches usable concurrently

Running one batch SHALL NOT depend on or change state shared with other batches of the same target, so that batches can run from concurrent workers.

#### Scenario: Two batches at once

- **WHEN** two batches of one target run concurrently
- **THEN** each returns the results of its own repos only, and the race detector reports no data race
