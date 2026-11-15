# Metrics

All metric names and attributes are defined in the OTel registry at [`registry/`](../registry/).

| Metric                                      | Instrument    | Description                                          |
| ------------------------------------------- | ------------- | ---------------------------------------------------- |
| `github.pr.count`                           | UpDownCounter | Open PR count by repo, state (open, draft) and label |
| `github.pr.age`                             | Gauge         | Age in seconds of the oldest open PR per repo        |
| `github.pr.created`                         | Counter       | PRs created                                          |
| `github.pr.merged`                          | Counter       | PRs merged                                           |
| `github.pr.closed`                          | Counter       | PRs closed without merge                             |
| `github.pr.close.duration`                  | Histogram     | Time from open to close/merge                        |
| `github.pr.lines.added`                     | Histogram     | Lines added per merged or closed PR                  |
| `github.pr.lines.removed`                   | Histogram     | Lines removed per merged or closed PR                |
| `github.pr.automerged`                      | Counter       | PRs merged with no human approval                    |
| `github.pr.review_status`                   | Gauge         | Open PR count by review decision state               |
| `github.issue.count`                        | UpDownCounter | Issue count by repo, state, and label                |
| `github.issue.age`                          | Gauge         | Age in seconds of the oldest open issue per repo     |
| `renovate.dashboard.queue`                  | Gauge         | Renovate dashboard PRs by queue state                |
| `github_exporter.scrape.duration`           | Histogram     | Collection cycle duration in seconds                 |
| `github_exporter.scrape.overruns`           | Counter       | Cycles longer than `collection_interval`             |
| `github_exporter.api.errors`                | Counter       | GitHub API errors by target and endpoint             |
| `github_exporter.api.secondary_rate_limits` | Counter       | GitHub secondary rate limit responses by target      |
| `github_exporter.otlp.errors`               | Counter       | OTLP push failures                                   |

## PR events

Each cycle the exporter reads every open PR of a repo, with no limit, for `github.pr.count`, `github.pr.age` and `github.pr.review_status`. It then reads PRs by last update, newest first, back to the start of the previous cycle's window, and counts the events of the selected PRs that it has not counted yet:

- `github.pr.created`, `github.pr.merged`, `github.pr.closed` (closed without merge; merged PRs count only in `github.pr.merged`), `github.pr.close.duration`, `github.pr.lines.added`, `github.pr.lines.removed` and `github.pr.automerged`.
- The window runs from the start of the repo's previous successful cycle, capped at 30 days back, to the start of the current cycle. Each event is counted once; a PR updated while the scan pages, and so missed, is counted by the next cycle.
- A repo's first cycle records no events. A failed cycle records none and the next successful cycle covers the gap.
- Counters reset when the exporter restarts. Events that happen while it is down are not counted.

Rates, globally and per repo:

```promql
sum(rate({"github.pr.merged"}[1h]))
sum by ("github.org", "github.repo") (increase({"github.pr.created"}[1d]))
```

Closed and merged totals are not exported as gauges. Use `increase({"github.pr.merged"}[30d])` instead of `{"github.pr.count", "github.pr.state"="merged"}`.

## PR size and author

`github.pr.lines.added` and `github.pr.lines.removed` record the additions and deletions GitHub reports for each merged or closed PR, on the same event as `github.pr.close.duration`, so each PR is recorded once. The values come with the event scan's query: no extra request, no extra GraphQL points. Bucket boundaries, shared by both: 0, 10, 50, 100, 250, 500, 1000, 2500, 5000, 10000 lines.

```promql
histogram_quantile(0.9, sum by (le, "github.repo") (rate({"github.pr.lines.added_bucket"}[1d])))
```

`collection.pr_author: true` (top level for every target, or per target under `targets[].collection`) adds `github.pr.author` to `github.pr.lines.added`, `github.pr.lines.removed` and `github.pr.close.duration`. It is off by default. The counters and gauges never carry it.

The value is the login the GraphQL API returns. For a GitHub App bot that is the app name without the `[bot]` suffix of its REST login: the Renovate app is recorded as `github.pr.author="renovate"`, though the `authors` filter selects it as `renovate[bot]`. A self-hosted bot that runs as a user account (for example `renovate-bot`) has the same login in both. A PR whose author account was deleted is recorded without the attribute.

Each author splits every repo's histogram series. A histogram is 13 series per attribute set (11 buckets, `_sum`, `_count`; 12 for `github.pr.close.duration`), so the setting adds 38 series per author and repo pair that merges or closes a PR. On one org of 392 repos, the 511 pairs seen in 30 days took the three histograms from 6,422 series to 19,418. Pairs are kept until the exporter restarts, not only for 30 days. Past 2,000 attribute sets on one histogram, counted across all targets of the process, the OpenTelemetry SDK records every new set in a single `otel.metric.overflow="true"` series with no other attribute: no author, target, org or repo. A target with the setting on can push the other targets' `github.pr.close.duration` into that series.

## Metric naming

The registry name is the stored name ([#14](https://github.com/gjed/github-activity-exporter/issues/14)). OTLP and the Prometheus bridge expose `github.pr.count` as `github.pr.count`: no `_total` or unit suffix (`_seconds`), no underscore escaping. Histograms add only the classic sample series `<name>_bucket`, `<name>_sum` and `<name>_count`, for example `github_exporter.scrape.duration_bucket`. CI checks both outputs against the registry.

Your backend must keep the names as sent (OTLP translation strategy `NoTranslation`); the settings per backend are in [Backend requirements](../README.md#backend-requirements). With any other setting the backend stores escaped or suffixed names (for example `github_pr_count`), which do not match the registry.

`/metrics` does not serve the OpenMetrics text format, because OpenMetrics requires counter names to end in `_total`. Scrapers that ask for OpenMetrics first, as Prometheus 3.x and Alloy do by default, get the Prometheus text format, which keeps the `counter` type. The name escaping comes from the `Accept` clause that matches: Prometheus 3.x also offers `text/plain;version=1.0.0;escaping=allow-utf-8` and gets `github.pr.count`. A scraper that offers only OpenMetrics, or an old text format, gets underscore-escaped names such as `github_pr_count`; add `PrometheusText1.0.0` to its `scrape_protocols`.
