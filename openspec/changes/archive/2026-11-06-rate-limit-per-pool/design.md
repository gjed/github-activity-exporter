# Design

## Context

`rateLimitTransport.RoundTrip` (`internal/github/client.go`) calls `waitIfPaused` before every request and `updateRateLimit` after every response that carries `X-RateLimit-Remaining`. Both read and write a single `rlState`/`rlPaused` pair. The same transport serves the go-github REST client and the githubv4 GraphQL client. The exporter calls REST `core` endpoints (issues, repos) and the GraphQL API; it makes no search calls today.

GitHub documents these pools in `GET /rate_limit` and [Rate limits for the REST API](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api): `core`, `search`, `code_search`, `graphql`, `integration_manifest`, `source_import`, `actions_runner_registration`, `scim`, `dependency_snapshots`, `dependency_sbom`, `code_scanning_upload`, `code_scanning_autofix`, `audit_log`. `X-RateLimit-Resource` names the pool a response counted against.

## Goals / Non-Goals

**Goals:**

- Pause a request only when the pool it will count against is low.
- Report the gauges per target and per pool, with bounded cardinality.

**Non-Goals:**

- Per-pool pause thresholds (see Decision 5).
- Secondary rate limits, `Retry-After`, or polling `GET /rate_limit`.
- Changing the auth-result hook or `/healthz`.

## Decisions

### 1. Pool classification before the request

The request's pool is derived from its URL, relative to the configured base URL:

| Request path (after the base URL path)                                       | Pool          |
| ---------------------------------------------------------------------------- | ------------- |
| GraphQL endpoint (`graphqlURL(base)`: `/graphql`, or `/api/graphql` on GHES) | `graphql`     |
| `/search/code`                                                               | `code_search` |
| `/search/...`                                                                | `search`      |
| anything else                                                                | `core`        |

The GraphQL test compares the full request path to the GraphQL URL's path, so a REST path that happens to end in `graphql` is not misclassified. The other documented pools are single endpoints the exporter never calls; they classify as `core` before the request. Alternative considered: a wrapper per client (REST vs GraphQL) passing the pool explicitly. Rejected: it does not separate `search` from `core`, and URL classification needs no change to how the clients are built.

### 2. Which pool a response updates

The `X-RateLimit-Resource` header is authoritative: GitHub states the response headers govern enforcement.

- Header present and a documented pool: state recorded under that pool, even if it differs from the classified pool.
- Header present, value not documented: recorded under `other`. Keeping the raw value would make the attribute unbounded; folding it into `core` would recreate the cross-pool pause this change removes. Nothing classifies into `other`, so it never pauses a request; it only shows up in the gauges.
- Header missing: recorded under the classified pool. Older GHES releases send `X-RateLimit-Remaining` without `X-RateLimit-Resource`; dropping the update would disable the pause there, which is a regression from today. The classified pool is the best available guess, and for the exporter's calls (REST `core`, GraphQL) it is the right one.
- No `X-RateLimit-Remaining` (e.g. GHES with rate limiting off): no update, as today.

### 3. State shape and API

`Client` keeps `map[string]*poolState` (state + paused flag) under the existing mutex; entries are created on first response for a pool. `RateLimitState` gains `Resource string`. `RateLimitHook` keeps its signature and receives the pool in the state. `Client.RateLimitState()` becomes `RateLimitState(resource string)` and returns the zero value for a pool not yet seen. The only callers are tests. Pause/resume log lines gain a `resource` field.

The existing logic per pool stays: malformed `X-RateLimit-Remaining` is ignored; a low pool without `X-RateLimit-Reset` gets the 60s fallback reset.

### 4. Metric attribute

`github.api.rate_limit.resource`: enum, `allow_custom_values: false`, one member per documented pool plus `other`. Required on both gauges, since the client always resolves a pool. Generated constants are `AttrGitHubApiRateLimitResource` and `AttrGitHubApiRateLimitResource<Pool>`; the attributes template needs `replace()` calls for the multi-word key part (`Ratelimit`) and members (`Codesearch`, `Integrationmanifest`, ...) so the Go names keep word casing. The selfmetrics method takes the resource and adds it to the attribute set, so each pool is its own series and a `core` update never overwrites `graphql`. The "zero reset keeps the last value" rule now applies per pool.

### 5. Threshold stays 200 for every pool

The issue specifies per-pool pausing with `remaining < 200`. That number fits `core` and `graphql` (5,000/h or more for authenticated callers). `search` (30/min) and `code_search` (10/min) are always below 200, so a search call would wait for the window reset on every request after the first. The exporter makes no search calls, so this is latent; it is marked `SIMPLIFIED:` in the code with the upgrade path (scale the threshold from `X-RateLimit-Limit`). Alternative: `min(200, limit/10)` now. Rejected for scope; it changes behavior for pools no caller uses.

## Risks / Trade-offs

- [Gauge series change shape: one series per target becomes one per target and pool] → Dashboards/alerts on these gauges must aggregate or filter on `github_api_rate_limit_resource`. The generated dashboard is regenerated from the registry. Recorded in the PR.
- \[A future endpoint in a pool the URL classifier maps to `core`\] → Its state is recorded under the right pool from the header, but the pre-request wait checks `core`. Acceptable until the exporter calls such an endpoint; extend the classifier then.
- [Missing header on GHES attributes to the classified pool] → Correct for the endpoints the exporter calls; documented in the code.

## Migration Plan

No config change. On upgrade the gauges gain the label; old unlabeled series go stale. Rollback is a revert.
