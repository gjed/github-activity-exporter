## 1. Filter Engine

- [x] 1.1 Implement `internal/filter/pr.go`: `PRFilter` struct with `Match(pr) bool` using label include/exclude and state rules
- [x] 1.2 Implement `internal/filter/issue.go`: `IssueFilter` struct with `Match(issue) bool` using title pattern and label rules
- [x] 1.3 Compile title patterns as `regexp.Regexp` at config load time; return error on invalid pattern
- [x] 1.4 Write unit tests for all filter combinations (table-driven)

## 2. PR Collector

- [x] 2.1 Define GraphQL v4 query for PRs: `number`, `state`, `createdAt`, `mergedAt`, `closedAt`, `labels{nodes{name}}`, `reviews{nodes{state}}`, `reviewDecision`
- [x] 2.2 Implement `internal/collector/pr.go`: `PRCollector.Collect(ctx, repos)` — paginate query, apply filter, build PR list
- [x] 2.3 Implement `github.pr.count` recording: count by state (open/merged/closed), using semconv constants
- [x] 2.4 Implement `github.pr.age` gauge: find oldest open PR per repo, record seconds since `createdAt`
- [x] 2.5 Implement `github.pr.close.duration` histogram: for each closed/merged PR within lookback window, record `closedAt - createdAt` in seconds
- [x] 2.6 Implement `github.pr.automerged` counter: PRs merged with no APPROVED review within lookback window
- [x] 2.7 Implement `github.pr.count` by label: for each PR, record once per label
- [x] 2.8 Implement `github.pr.review_status` gauge: count open PRs by `reviewDecision`
- [x] 2.9 Implement configurable `max_prs_per_repo` limit and `lookback_days` window
- [x] 2.10 Write unit tests using fixture PR data (table-driven, no network calls)

## 3. Issue Collector

- [x] 3.1 Implement `internal/collector/issue.go`: `IssueCollector.Collect(ctx, repos)` — REST issue listing, paginated, apply filter
- [x] 3.2 Implement `github.issue.count` recording: by state and label
- [x] 3.3 Implement `github.issue.age` gauge: oldest open issue per repo after filters applied
- [x] 3.4 Write unit tests using fixture issue data

## 4. Dependency Dashboard Parser

- [x] 4.1 Implement `internal/collector/dashboard.go`: `DashboardCollector.Collect(ctx, repos)`
- [x] 4.2 Implement issue identification: filter issues by title `"Dependency Dashboard"` and author == configured bot account
- [x] 4.3 Implement section parser: regex `^## (Awaiting Schedule|Rate-Limited|Pending Approval|Open)\s*$` to find section boundaries
- [x] 4.4 Implement entry counter: count `- [x]` and `- [ ]` list items within each section boundary
- [x] 4.5 Record `renovate.dashboard.queue` gauge per section using semconv constants
- [x] 4.6 Record `renovate.dashboard.parse_error` = 1 when no expected sections found, 0 on success
- [x] 4.7 Write unit tests with fixture dashboard bodies (multiple known Renovate output formats, edge cases: empty sections, extra sections, non-dashboard issue)

## 5. Collection Loop

- [x] 5.1 Implement `internal/collector/runner.go`: `Runner.Run(ctx)` — drives all collectors for all repos in a target on each cycle
- [x] 5.2 Wire runner into OTel `PeriodicReader` collection callback (called by the SDK on each interval)
- [x] 5.3 Implement per-target goroutine isolation: one `Runner` per target, each with its own GitHub client and metric instruments
- [x] 5.4 Implement graceful shutdown: cancel context on SIGTERM, wait for in-flight collections to complete
- [x] 5.5 Write integration test: mock GitHub API server returning fixture data, start full collection loop, assert correct metric values recorded
