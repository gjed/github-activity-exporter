# Proposal

## Why

The issue collector lists `GET /repos/{owner}/{repo}/issues?state=all` for every repo on every cycle, and the Dependency Dashboard collector lists every open item to find one issue ([#93](https://github.com/gjed/github-activity-exporter/issues/93)). The REST endpoint returns pull requests too, which both collectors discard. On a Renovate-heavy org almost every page fetched is PRs, so the cost grows with PR history instead of with issues: zextras/tech-doc has 16 issues and 1669 PRs, which is 17 REST pages per cycle for 16 issues.

## What Changes

- The issue collector reads issues through GraphQL `repository.issues`, which returns issues only. Per-state counts come from `totalCount`, per-label counts from each repo label's issue `totalCount`, and the oldest open issue from one node ordered by creation date. No PR is ever fetched.
- When a target sets `filters.issues.exclude_title_patterns`, counts depend on titles, so the collector pages the repo's issues (issues only, server-side state filter) and applies the patterns client-side.
- `filters.issues.states` keeps working: it is applied server-side to every query.
- The Dependency Dashboard collector finds the dashboard with one GraphQL query: open issues filtered by `createdBy: <configured bot login>`, matched on title "Dependency Dashboard".
- Bot logins: the configured login stays in REST form (`renovate[bot]`, the existing `RENOVATE_BOT_LOGIN` default). GraphQL `createdBy` accepts that form, but returns a bot author as `login: "renovate"` with `__typename: Bot`; the author check normalises it back to `renovate[bot]`.
- Both collectors move from the REST `core` rate-limit pool to the `graphql` pool. `github_exporter.api.errors` records their failures with `github.api.endpoint="graphql"`; the now unused `rest.issues` member is removed from the registry.
- No exporter request goes to REST `/repos/{owner}/{repo}/issues` any more, so that route is removed from the `url.template` routes of the HTTP client metrics.
- Metric values of `github.issue.count`, `github.issue.age` and `renovate.dashboard.*` do not change.

## Capabilities

### New Capabilities

- `issue-collection`: how the exporter queries a repo's issues and turns them into `github.issue.count` and `github.issue.age`, honouring the issue filters, without fetching pull requests.

### Modified Capabilities

- `pr-collection`: adds a requirement that the Renovate Dependency Dashboard is found with a creator-filtered query that does not page through pull requests, with the bot login format rules.
- `self-telemetry`: the REST issues route leaves the `url.template` routes; the examples in the HTTP client metric requirements use `/orgs/{org}/repos`.

## Impact

- `internal/collector/issue.go`, `internal/collector/dashboard.go` and their tests; `cmd/github-activity-exporter/main.go` wiring (GraphQL client instead of REST issues service) and `main_test.go`.
- `internal/collector/runner.go`: API error endpoint for both collectors.
- `registry/attributes.yaml` (`github.api.endpoint` members), `templates/go/attributes.go.j2` casing replace, regenerated `internal/semconv`.
- `internal/github/transport.go` route list and its tests, `tests/integration/self_telemetry_test.go`, README `url.template` route list.
- `tests/integration` fixtures and `tests/e2e` mock GitHub: serve the new GraphQL queries instead of the REST issues list.
- Operators: REST `core` usage drops by the issue and dashboard pages; `graphql` usage rises by about one point per repo per collector. `github_exporter.api.errors{github.api.endpoint="rest.issues"}` series stop; the failures appear under `graphql`.
