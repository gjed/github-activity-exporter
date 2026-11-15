# Collection and rate limits

## Collection schedule

Each target starts a collection cycle every `collection_interval`, measured from the start of its previous cycle, and pushes the cycle's metrics when the cycle ends. A cycle that runs longer than `collection_interval` is an overrun: the exporter logs a warning, increments `github_exporter.scrape.overruns` and starts the next cycle as soon as the late one finishes. Cycles of one target never overlap, and missed periods are not caught up. Steady overruns mean the configured freshness is out of reach; raise `collection.concurrency` (see [Collection concurrency](#collection-concurrency)) or `collection_interval`.

Between cycles the exporter also pushes every `export.interval` (default `60s`, must be positive), whatever `collection_interval` is. Each push carries the latest values of every series, so series stay continuous with a `collection_interval` of 15m or more. Counters and histograms are pushed with cumulative temporality: a push repeats the running total and never adds to it.

## Collection concurrency

A cycle is a sequence of GitHub requests, each of which takes about 0.5 to 0.7 s, so on a large org it is latency, not rate limit budget, that sets the cycle length. `collection.concurrency` (default `4`, 1 to 100) is how many units of work a target runs at once. A unit is one repo of the PR scan, one batch of the issue count or Dependency Dashboard query (`graphql_batch_size` repos), or one repo of the issue listing used with `exclude_title_patterns`. A unit sends its requests one after another, so a target never has more than `concurrency` collection requests in flight. The PR, issue and dashboard collectors run at the same time and share that limit. `1` sends one request at a time. The exported series do not depend on the setting. Set it at the top level for every target, or per target under `targets[].collection.concurrency`.

Measured against one 390-repo org with a PAT, Dependency Dashboard enabled, one cycle per run:

| `concurrency` | Cycle (`scrape.duration`) | GraphQL requests | GraphQL points | REST requests | 403/429 responses |
| ------------: | ------------------------: | ---------------: | -------------: | ------------: | ----------------: |
|             1 |                   286.7 s |              431 |            581 |             6 |                 0 |
|             4 |                    73.4 s |              431 |            591 |             6 |                 0 |
|             8 |                    40.4 s |              431 |            605 |             6 |                 0 |

To size it:

1. Check the cycle length against `collection_interval`. `github_exporter.scrape.duration` (sum over count, or a quantile) should stay well below `collection_interval`; a rising `github_exporter.scrape.overruns` means it does not. Cycle time falls roughly as `1 / concurrency` until GitHub's secondary rate limits slow it down.
1. Check the budget. Concurrency does not change the points one cycle spends (see [GraphQL cost](#graphql-cost)), but a shorter cycle allows a shorter `collection_interval`, and points per hour are points per cycle times cycles per hour. Read the drop of `github_exporter.api.rate_limit.remaining{github.api.rate_limit.resource="graphql"}` over one cycle, and keep points per hour well under the pool's limit (5,000 for a PAT, 12,500 or more for a GitHub App installation). Below 200 remaining, the exporter pauses that pool until `github_exporter.api.rate_limit.reset`.
1. Stay under GitHub's [secondary rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api#about-secondary-rate-limits): at most 100 concurrent requests and about 2,000 secondary rate limit points per minute per token (one per GraphQL query request, unrelated to the primary points above), shared by every target and every other client using the same token. At about 0.6 s per request, each unit of concurrency sends up to about 100 GraphQL requests per minute, so values above about 16 add little and risk hitting the limit.

On a secondary rate limit response (403 or 429 with `Retry-After`, or a 429 or 403 whose body names a secondary rate limit), the exporter pauses every request of the target until the time GitHub gives, 60 s doubling per further hit when it gives none, then retries the request up to 3 times. Each such response increments `github_exporter.api.secondary_rate_limits`. A primary limit response (`X-RateLimit-Remaining: 0`) pauses only its pool until the reset, then retries. A steadily rising `github_exporter.api.secondary_rate_limits` means `concurrency` is too high for the token: lower it.

## GraphQL cost

PR, issue and Dependency Dashboard collection all use GraphQL and share the `graphql` rate-limit pool, which is billed in points. Per repo and per cycle, PR collection costs at least 2 points per page: the open-PR scan, and from the second cycle on the event scan. Issue counts and the Dependency Dashboard lookup read `graphql_batch_size` repos per query, at 1 point per batch; a repo with more than 100 labels, or more than 100 open issues by the Renovate bot, reads its further pages on its own, 1 point each. With `exclude_title_patterns` set, issue collection lists every repo's issues instead, 1 point per 100 issues per repo. A token gets 5,000 points per hour; a GitHub App installation gets 5,000 plus 50 per repo and per org member beyond 20, capped at 12,500, or 10,000 on a GitHub Enterprise Cloud organization ([GitHub rate limits](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api)). Size `collection_interval` so that the points per cycle × cycles per hour stay below the limit. Measured on an org of 389 repos with the dashboard enabled, one cycle costs 819 points without the event scan (778 for PR scans, 41 for issues and dashboard), at least 1,597 with it: at the default 5m, at least 19,164 points per hour, which needs a longer interval with either a token or an App. When fewer than 200 points remain, the pool pauses until it resets and PR, issue and dashboard collection all wait.

`graphql_batch_size` trades request count for query time. GitHub fails a query that exceeds its per-query resource limits; the issue count query takes about 0.15s per repo and hit those limits at 50 repos. A repo GitHub answers with a resource-limit error, or a batch that fails as a whole (HTTP 502, timeout), is read again one repo per request, so a too-large value costs points, not data. `1` restores one query per repo.
