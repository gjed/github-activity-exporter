# Tasks

## 1. Baseline

- [ ] 1.1 Before changing code, run the current issue and Dependency Dashboard collectors against zextras/tech-doc and zextras/carbonio-mails-ui (bot login `ZxBot`, `GITHUB_TOKEN=$(gh auth token)`) through a throwaway, uncommitted harness that counts requests per rate-limit pool and reads the `graphql` used count from `GET /rate_limit` before and after; record REST calls, GraphQL points and every `github.issue.*` and `renovate.dashboard.*` value

## 2. Issue collector

- [ ] 2.1 Replace the REST listing in `internal/collector/issue.go` with the `totalCount` query from design.md (state counts, oldest open issue, paged label counts, `$states` from `filters.issues.states`); verify with unit tests on a fake GraphQL client: a fixture repo with 2000 PRs and 3 issues makes one request and no PR query, state and label counts and age match the expected values, zero counts are not emitted, `states: [closed]` emits no open count and no age
- [ ] 2.2 Add the paged path for `exclude_title_patterns` (issues only, server-side states); verify with unit tests: excluded titles are dropped from state and label counts, an excluded oldest issue moves `github.issue.age` to the next one, multiple pages are followed
- [ ] 2.3 Wire `ghc.GraphQL()` into the issue collector in `cmd/github-activity-exporter/main.go`; verify `go build ./...`

## 3. Dashboard collector

- [ ] 3.1 Replace `findDashboardIssue` in `internal/collector/dashboard.go` with the `createdBy` query and normalise `Bot` authors with the PR collector's `[bot]` rule; verify with unit tests: dashboard found with one request in a repo whose fixture holds 500 open PRs, `createdBy` sent as `renovate[bot]`, a `Bot` author `renovate` matches `renovate[bot]`, a `User` author `ZxBot` matches `zxbot`, configured `renovate` does not match a `Bot` author `renovate`, an empty login sends no `createdBy`, no dashboard emits nothing
- [ ] 3.2 Wire `ghc.GraphQL()` into the dashboard collector in `main.go` and update `main_test.go`; verify `go test ./cmd/...`

## 4. Rate-limit pool and API errors

- [ ] 4.1 Record issue and dashboard failures as `github.api.endpoint="graphql"` in `internal/collector/runner.go`; remove the `rest_issues` member from `registry/attributes.yaml` and its casing replace from `templates/go/attributes.go.j2`, widen the `graphql` brief, run `make generate`; verify `make check-schema` and a runner test asserting the endpoint value

- [ ] 4.2 Add a test that runs both collectors through the real `internal/github` client against an `httptest` GitHub returning `X-RateLimit-Resource: graphql`; verify every request hits `/graphql`, the rate-limit hook reports the `graphql` pool and no `core` request is made

- [ ] 4.3 Remove `/repos/{owner}/{repo}/issues` from `restRoutes` in `internal/github/transport.go` and the README `url.template` route list, and move the transport and `tests/integration/self_telemetry_test.go` examples to `/orgs/{org}/repos`; verify `go test ./internal/github/... ./tests/integration/...`

## 5. Integration fixtures

- [ ] 5.1 Serve the issue and dashboard GraphQL queries from `tests/integration` fixtures and the `tests/e2e` mock GitHub (keep the one-time failure on repo-b, now on the issue query) and drop the REST issues handler; verify `go test ./...` and `make test-e2e`

## 6. Validation

- [ ] 6.1 Run `go build ./...`, `go test ./...`, `make check-schema`, the OTLP and `/metrics` live-checks and the dashboard e2e; verify all pass
- [ ] 6.2 Re-run the harness from 1.1 on the same repos; verify identical metric values and report REST calls and GraphQL points per cycle, before and after

## Workflow follow-up

- Run `openspec archive issue-collection-graphql` before the final commit, in the same PR.
- Open the PR with body "Closes #93", assignee gjed.
