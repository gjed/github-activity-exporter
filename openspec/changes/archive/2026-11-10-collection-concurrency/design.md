# Design

## Context

See proposal.md for motivation. On current main (`339c0d4`):

- `Runner.collect` (`internal/collector/runner.go`) discovers repos, then calls `PRCollector.Collect`, `IssueCollector.Collect` and `DashboardCollector.Collect` one after another, each over the full repo list, then swaps the three snapshots and `lastCollectedAt` under `r.mu`, adds `github_exporter.api.errors` per failed repo, records `github_exporter.scrape.duration`, and flushes.
- `PRCollector.Collect` (`pr.go`) loops over repos: reads `c.windows[repo]`, runs the open scan and the event scan (each paged serially), records event counters, writes `c.windows[repo]`, appends to `results` or `failed`.
- `IssueCollector.Collect` and `DashboardCollector.Collect` loop over `batches(repos, batchSize)` and call `countBatch` / `findBatch` (`batch.go` `runRepoBatch`), which already keep no shared state ([graphql-batching: Batches usable concurrently](../../specs/graphql-batching/spec.md)). The issue title-filter listing pages each repo serially.
- `rateLimitTransport.RoundTrip` (`internal/github/client.go`) waits on the request's pool (`waitIfPaused`), sends, reports 401 to the auth hook, and records `X-RateLimit-*` per pool under `c.mu`. A 403 or 429 is returned to the caller as is: the repo fails and is counted in `api.errors`. There is no secondary rate limit handling.
- `otelhttp` sits under `rateLimitTransport` (`transport.go`), so each round trip is one `http.client.request.duration` observation.

## Measurements

Read-only, against `zextras` with a PAT (`GITHUB_TOKEN=$(gh auth token)`), Dependency Dashboard enabled, one cycle of a binary built from `339c0d4`:

| Measure                                                             |       Value |
| ------------------------------------------------------------------- | ----------: |
| Repos discovered                                                    |         390 |
| Cycle duration (`github_exporter.scrape.duration`)                  |     301.7 s |
| GraphQL requests (`http.client.request.duration` count, `/graphql`) |         431 |
| REST requests (`/orgs/{org}/repos`, plus `/user` at startup)        |       6 + 1 |
| GraphQL points (`rateLimit.remaining` 4,999 to 4,332)               |   about 667 |
| Mean GraphQL round trip                                             | about 0.6 s |
| `github_exporter.api.errors`                                        |           0 |

The cycle is 431 serial round trips of about 0.6 to 0.7 s: it already exceeds the 5m default `collection_interval` on this org, while it spends about 13% of a PAT's hourly `graphql` budget per cycle. The issue's v0.3.0 figures (1,043 s, 1,180 points) predate the fixed period ([#95](https://github.com/gjed/github-activity-exporter/issues/95)) and batching ([#104](https://github.com/gjed/github-activity-exporter/issues/104)); the PR scans are now about 390 of the 431 requests.

After the change, same org, same token, one cycle per run, run back to back:

| `concurrency` | Cycle (`scrape.duration`) | GraphQL requests | GraphQL points | REST requests | 403/429 responses |
| ------------: | ------------------------: | ---------------: | -------------: | ------------: | ----------------: |
|             1 |                   286.7 s |              431 |            581 |             6 |                 0 |
|             4 |                    73.4 s |              431 |            591 |             6 |                 0 |
|             8 |                    40.4 s |              431 |            605 |             6 |                 0 |

REST requests are the 6 pages of repo discovery; the `core` pool is otherwise unused. GraphQL points are the change of `rateLimit.used` over the run, less the 1 point of the reading query; the run at 8 is measured from the reading after the run at 4, because GitHub answered the reading before it from a different window. No run had a 403 or 429 or a secondary rate limit, and the three runs exported the same 2,409 product series with the same values (`github.issue.age` and `github.pr.age` values left out, as they move with the clock).

## Goals / Non-Goals

**Goals:**

- Cycle duration divided roughly by `concurrency` up to GitHub's secondary limits.
- Identical exported series for every `concurrency`.
- A secondary rate limit slows the whole target down instead of failing repos.

**Non-Goals:**

- Concurrent paging inside one repo, concurrent discovery, or concurrency across targets (targets already run in their own goroutines).
- Adaptive concurrency or client-side points-per-minute throttling.
- Dashboard changes beyond the secondary rate limit panel ([#90](https://github.com/gjed/github-activity-exporter/issues/90) owns the dashboards; see Rollout).

## Decisions

### Shared per-target limiter

A `collector.Limiter` (buffered channel semaphore) is created per target in `main.go` from the resolved `collection.concurrency` and passed to the three collectors of that target. `Limiter.each(ctx, n, fn)` runs `fn(i)` for `i` in `[0, n)`: it acquires a slot before starting each goroutine (so at most `concurrency` goroutines per target do work), stops starting new ones once `ctx` is done, and waits for the started ones. A nil limiter means 1.

The unit is a repo (PR collector, issue listing) or a batch (issue counts, dashboard lookup). A unit's requests stay serial (pages need the previous cursor; per-repo fallbacks follow their batch), so in-flight collection requests per target are bounded by `concurrency`. Discovery refresh and App token exchange run outside the limiter, as today.

Plumbing: `BatchOption func(*int)` becomes `CollectorOption func(*collectorOptions)` holding batch size and limiter; `WithGraphQLBatchSize` keeps its name, `WithLimiter` is added; `PRCollectorConfig` gets a `Limiter` field.

Alternatives: a limiter owned by the Runner and passed to `Collect` changes every `Collect` call site in tests for no behavioural gain; a limiter in the context hides a dependency.

### Collectors run concurrently

`Runner.collect` starts the three collectors in goroutines and waits for all of them. Each writes only its own result variable; everything after the wait (snapshot swap, `lastCollectedAt`, `api.errors`, scrape duration, flush) is unchanged. Without this, the issue and dashboard phases (about 20 batches each on a 400-repo org) would leave workers idle at the end of each phase. At `concurrency: 1` requests stay one at a time; the three collectors' requests interleave, which changes request order but no output.

### Merge in repo order

Workers write their result to slot `i` of a slice sized to the repo or batch list. After `each` returns, the collector walks the slots in order and builds `results` and `failed` exactly as the serial loop did. For the PR collector, the per-repo inputs (`since`, `scanFrom`, `counted` from `c.windows`) are read before dispatch, and event recording (`recordEvents`) and `c.windows` updates happen in the merge loop, so `c.windows` is never touched by a worker. Output order, `FailedRepos()` order and window contents are then identical to concurrency 1.

`lastCollectedAt` is already copied under `r.mu` before collection and swapped after; workers only read the copy. `observableCallback` reads the snapshot pointers under `r.mu.RLock`, unchanged. `ForceFlush` still runs once after the cycle.

### Secondary rate limits in the transport

`rateLimitTransport.RoundTrip` becomes a loop of at most 4 attempts:

1. Wait on the target gate (`c.pausedUntil`, under `c.mu`), then on the request's pool (`waitIfPaused`, unchanged). Both waits return on `ctx.Done()`.
1. Send, report the 401 result, record `X-RateLimit-*` (unchanged).
1. Classify a 403 or 429:
   - `X-RateLimit-Remaining: 0`: primary exhaustion. The pool is already paused by step 2 until `X-RateLimit-Reset` (or the existing 60 s fallback); retry.
   - `Retry-After` present (seconds, or an HTTP date): secondary; pause the target for that long.
   - 429, or a 403 whose body (first 64 KiB) contains `secondary rate limit` case-insensitively: secondary; pause the target for 60 s × 2^(attempt-1).
   - Otherwise: not a rate limit, return the response.
1. A secondary response calls the secondary rate limit hook (counted per response) and logs a warning with status, pool and wait. The pause extends `pausedUntil` to `max(pausedUntil, now+wait)`, so concurrent workers that are rate limited at once do not shorten each other's pause.
1. Retry with `req.Clone` and a fresh body from `req.GetBody`. When the attempts are spent, or the request has a body but no `GetBody`, return the last response with its body restored.

go-github and `QueryRaw`/githubv4 build their requests from byte buffers, so `GetBody` is set. Waits go through a `Client` clock field so tests do not sleep. Since `otelhttp` is under this transport, each attempt is still recorded with its `error.type`, and every attempt is reported to the 401 hook once.

The target gate is per `Client`, and there is one `Client` per target, so other targets are unaffected. Primary exhaustion keeps the per-pool scope of the existing "Low pool does not pause other pools" scenario.

Alternative: retry in the collectors. Rejected: the pause must hold every worker of the target, which only the shared transport sees.

### Self-metric

`github_exporter.api.secondary_rate_limits`, counter, unit `{response}`, attribute `exporter.target` (required), declared in `registry/signals.yaml` with `annotations.prometheus`, constants from `make generate`. Pool is not an attribute: secondary limits are not per pool. `SelfMetrics` gets `RecordSecondaryRateLimit` and `InitSecondaryRateLimits` (0 at loop start, as `InitOverruns`), wired through `ghclient.WithSecondaryRateLimitHook` in `main.go`.

### Config

`collection.concurrency` at the top level (`Config.Collection.Concurrency`) and per target (`Target.Collection.Concurrency`), both `*int`. `Load` resolves each target's value: target, else global, else 4. Validation, 1 to 100, names the target for a target value. 100 is GitHub's documented limit on concurrent requests; the README explains why a much lower value is the practical ceiling.

## Risks / Trade-offs

- [Secondary limits at high concurrency] GraphQL allows about 2,000 points per minute per user; with about 0.6 s per request, 16 workers reach about 1,600 requests per minute. → Default 4; docs give the sizing; the counter shows hits; the target-wide pause stops the burst instead of failing repos.
- [Primary budget burns faster] Higher concurrency with a short `collection_interval` runs more cycles per hour. → The existing per-pool pause at 200 remaining still applies; docs size `collection_interval` against the `rate_limit.remaining` gauge.
- [Retried requests lengthen a cycle] A long `Retry-After` can push a cycle past `collection_interval`. → Counted as an overrun, as today; `stall_timeout` (default 2 × interval + 1h) is far above GitHub's typical waits.
- [Interleaved order at concurrency 1] Request order differs from main at `concurrency: 1`. → No output difference; covered by the serial vs concurrent test.

## Rollout

Default 4 changes behaviour for existing configs on upgrade; `collection.concurrency: 1` restores serial collection. [#90](https://github.com/gjed/github-activity-exporter/issues/90) merged during review: the branch is rebased on it and adds a `github_exporter.api.secondary_rate_limits` panel to its exporter dashboard.

## Open Questions

None.
