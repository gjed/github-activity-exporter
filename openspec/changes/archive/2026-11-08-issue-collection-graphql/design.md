# Design

## Context

`IssueCollector.fetchIssues` (`internal/collector/issue.go`) pages `GET /repos/{owner}/{repo}/issues?state=all` and drops every item with `IsPullRequest()`. `DashboardCollector.findDashboardIssue` (`internal/collector/dashboard.go`) pages `GET .../issues?state=open` until an item titled "Dependency Dashboard" by the bot login turns up. Both go through the REST `core` pool. The PR collector already uses GraphQL through the same authenticated, rate-limit-hooked transport (`internal/github`), which classifies a request's pool by URL path (`/graphql` → `graphql`).

Facts checked against the live API on 2026-11-08:

- `repository.issues` returns issues only; `totalCount` per state costs 1 point. One query with `issues(states: OPEN){totalCount}`, `issues(states: CLOSED){totalCount}`, the oldest open issue and `labels(first: 100){ nodes{ name issues(states: $states){totalCount} } }` costs 1 point on zextras/tech-doc (1669 PRs, 16 issues).
- `issues(filterBy: {createdBy: "renovate[bot]"})` on renovatebot/renovate returns the dashboard, with `author { __typename: "Bot", login: "renovate" }`. `createdBy: "renovate"` and `createdBy: "app/renovate"` return nothing.
- REST `creator=renovate[bot]&state=open` on the same repo returns 11 items, 10 of them PRs: the REST creator filter still pages PRs.

## Goals / Non-Goals

**Goals:**

- No request made by the issue or dashboard collector returns a pull request.
- Unchanged `github.issue.count`, `github.issue.age` and `renovate.dashboard.*` values.

**Non-Goals:**

- Batching several repos into one GraphQL query.
- Changing the dashboard title match, metric names or attributes, or the `RENOVATE_BOT_LOGIN` setting.
- Changing the PR collector or repo discovery.

## Decisions

### Issue counts from `totalCount` (no title patterns)

One query per repo, `$states` set from `filters.issues.states` (nil = both):

- `open: issues(states: OPEN){totalCount}`, `closed: issues(states: CLOSED){totalCount}`, each used only when its state passes the filter, and emitted only when non-zero (the current code creates a map entry on the first match).
- `oldest: issues(first: 1, states: OPEN, orderBy: {field: CREATED_AT, direction: ASC}){ nodes{ createdAt } }` for `github.issue.age`, used only when `open` passes the filter.
- `labels(first: 100, after: $cursor){ nodes{ name issues(states: $states){totalCount} } pageInfo }`, non-zero counts only. A repo with more than 100 labels pages the same query; the extra counts cost nothing.

`Label.issues` counts issues only, across the given states, which is what the current per-issue label tally computes.

Alternative: page all issues and count client-side in every case. Rejected: cost grows with the issue history again, and the issue asks for `totalCount`.

### Paging issues when title patterns are set

`exclude_title_patterns` are client-side regexps; `totalCount` cannot subtract matches. With patterns set, the collector pages `issues(first: 100, states: $states, after: $cursor){ nodes{ state title createdAt labels(first: 100){ nodes{ name } } } }` and computes the same stats as today with the existing `filter.IssueFilter`. Still issues only: zextras/tech-doc is one request instead of 17. GitHub caps an issue at 100 labels, so `labels(first: 100)` reads all of them.

Alternative: fetch counts by `totalCount` and page only the issues whose title matches. Rejected: GitHub search cannot evaluate a regexp, so finding the matches means reading every title anyway.

### Dashboard lookup with `createdBy`

One query per repo: `issues(first: 100, states: OPEN, filterBy: {createdBy: $login}, after: $cursor){ nodes{ title body author{ __typename login } } pageInfo }`, then the existing title and author check. The bot opens a handful of issues per repo, so this is one request in practice; paging stays for correctness. An empty login sends no `createdBy` and keeps the current "any author" behaviour.

Alternative: REST `creator=<login>`. Rejected: the REST list includes the bot's PRs, so a repo with many open Renovate PRs still pages them.

### Bot login format

The configured login stays in REST form, `renovate[bot]`, which is also what `createdBy` accepts. GraphQL reports a bot author as `login: "renovate"`, `__typename: "Bot"`. The author check reuses the PR collector's rule (`authorLogin` in `pr.go`): a `Bot` author without the suffix gets `[bot]` appended, then compares case-insensitively with the configured login. A configured bare `renovate` therefore matches neither the query nor the check, the same as REST `creator=renovate` today. Tests cover a `Bot` author, a `User` author (`ZxBot`) and the bare-login mismatch.

### Rate-limit pool and API error endpoint

`main.go` passes `ghc.GraphQL()` to both collectors instead of `ghc.REST().Issues`. The transport already classifies `/graphql` requests into the `graphql` pool and feeds the rate-limit hook, so no change in `internal/github`. The runner records issue and dashboard failures as `github.api.endpoint="graphql"`. The `rest_issues` member of `github.api.endpoint` has no caller left and is removed from `registry/attributes.yaml`, with its `Restissues` casing replace in `templates/go/attributes.go.j2`; the `graphql` member brief is widened to cover issues and the dashboard.

### Issues `url.template` route

With both collectors on GraphQL, no exporter request goes to `/repos/{owner}/{repo}/issues`. The route is removed from `restRoutes` in `internal/github/transport.go`, from the README route list and from the self-telemetry spec examples, which now use `/orgs/{org}/repos`. A request to that path would get no `url.template`, like any other unknown route.

## Risks / Trade-offs

- [GraphQL pool load] → about one point per repo per collector per cycle moves from `core` (several requests per repo) to `graphql` (5000 points/hour). On the 389-repo org of #93 that is under 800 points per cycle.
- \[`api.errors` label value change\] → queries on `github.api.endpoint="rest.issues"` go empty; the same failures are counted under `graphql`.
- [Counting differences between REST and GraphQL] → checked by running both versions against the same zextras repos and comparing the metric values.
