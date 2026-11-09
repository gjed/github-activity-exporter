# Tasks

## 1. Raw GraphQL request

- [ ] 1.1 Add `(*Client).QueryRaw` and `RawResponse`/`GraphQLError` in `internal/github/client.go`: POST through the client's `http.Client`, return per-key `data` and errors with `message`, `type`, `path`; non-200 and undecodable bodies are errors; verify with httptest unit tests for success, partial errors and non-200
- [ ] 1.2 Record `data.rateLimit` `remaining`/`resetAt` on the `graphql` pool via `updateRateLimit` and log `cost` at debug; verify with a unit test that a body with `remaining: 150` pauses the pool and fires the rate-limit hook

## 2. Config

- [ ] 2.1 Add `graphql_batch_size` to `config.Target` with default 20 and range 1 to 100, failing with an error naming the target; verify with config unit tests for unset, 1, 100, 0 and 101

## 3. Batch helper and collectors

- [ ] 3.1 Add the collector-side batch helper: builds the aliased query with positional aliases and `$oI`/`$nI` variables, maps errors by `path[0]` to repos, classifies resource-limit, repo and batch-wide failures; verify with unit tests for names like `.github`, alias mapping order, nested `RESOURCE_LIMITS_EXCEEDED`, `NOT_FOUND` and no-path errors
- [ ] 3.2 Switch `IssueCollector` count mode to batches, with per-repo continuation from `endCursor` for repos with more than 100 labels and per-repo retry on resource-limit and batch-wide failures; title-pattern listing unchanged; verify `go test ./internal/collector/...` including a test that batched and `graphql_batch_size: 1` results are equal
- [ ] 3.3 Switch `DashboardCollector` lookup to batches with the same fallback rules; verify with dashboard tests for found, not found, next page and a failed alias
- [ ] 3.4 Extend the issue and PR fixtures and the e2e mock GitHub (`tests/e2e/mockgithub_test.go`) to answer batched queries per alias; verify `go test ./...` and `go test -race ./internal/collector/...` pass, with a test running two batches concurrently
- [ ] 3.5 Wire `graphql_batch_size` and the client into the collectors in `cmd/github-activity-exporter/main.go`; verify `go build ./...` and `main_test.go`

## 4. Documentation

- [ ] 4.1 Document `graphql_batch_size` in the README configuration example and rewrite the GraphQL cost paragraph with the per-batch costs and the measured `zextras` numbers; verify with `pre-commit run --config ~/.config/pre-commit/.pre-commit-config.yaml --files README.md`

## 5. Validation

- [ ] 5.1 Run `go build ./... && go test ./... && make check-schema` and `make generate` with no diff
- [ ] 5.2 Run the OTLP and `/metrics` live-checks (`go test -tags integration ./tests/integration/...`) and the dashboard e2e (`make test-e2e`)
- [ ] 5.3 Measure before (origin/main) and after against `zextras` read-only with `GITHUB_TOKEN=$(gh auth token)`, dashboard enabled: GraphQL points per cycle (summed `rateLimit.cost` and the `graphql` remaining gauge delta), REST calls per cycle (`core` gauge delta), cycle duration (`github_exporter.scrape.duration`); diff the exported series of both runs, excluding `github.issue.age` values, and confirm they are identical

## Workflow follow-up

- Run `openspec archive graphql-batching` before the final commit, in the same PR.
- Open the PR with "Closes #104", assignee gjed, after diff approval; re-check `gh pr view --json state` before any push.
