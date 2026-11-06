# Tasks

## 1. Registry and generated code

- [ ] 1.1 Add the enum attribute `github.api.rate_limit.resource` (documented pools plus `other`, `allow_custom_values: false`) to `registry/attributes.yaml`; reference it as required on both rate limit gauges in `registry/signals.yaml`. Verify: `make check-schema` passes, including the `annotations.prometheus` policy.
- [ ] 1.2 Add the casing `replace()` calls to `templates/go/attributes.go.j2` and run `make generate`. Verify: `internal/semconv/attributes.go` has `AttrGitHubApiRateLimitResource` and word-cased member constants; no other unexpected diff.

## 2. GitHub client

- [ ] 2.1 Classify the pool of each request from its URL (`graphql`, `code_search`, `search`, `core`) and keep rate limit state and the pause flag per pool in `internal/github/client.go`; record responses under `X-RateLimit-Resource`, `other` for undocumented values, the classified pool when the header is missing. Add `Resource` to `RateLimitState`, take the pool in the state accessor, add the pool to pause/resume logs, mark the fixed threshold `SIMPLIFIED:`. Verify: `go build ./...`.
- [ ] 2.2 Tests in `internal/github/client_test.go`: low `graphql` does not pause REST; low `core` does not pause GraphQL; a missing header records under the classified pool; an undocumented value records under `other` and pauses nothing; the hook reports the pool; classification of GraphQL (github.com and GHES base URLs), search and code search paths. Existing tests updated for the accessor. Verify: `go test ./internal/github/...`, including the unchanged auth-result tests.

## 3. Self-metrics and wiring

- [ ] 3.1 `RecordRateLimit` takes the pool and adds `github.api.rate_limit.resource`; `cmd/github-activity-exporter/main.go` passes the hook's pool. Verify: `go build ./...`.
- [ ] 3.2 Self-metrics test: alternating `core`/`graphql` records yield one series per pool with their own last values, and the zero-reset rule holds per pool; update `tests/integration/live_check_test.go` to record the gauges with the attribute. Verify: `go test ./...`, then the OTLP and `/metrics` live-check runs (`make test-integration`) report no violations.

## 4. Final validation

- [ ] 4.1 Run `go build ./...`, `go test ./...`, `make check-schema`, `make generate` (no diff), and the live-check runs; paste the output in the PR review.

## Workflow follow-up

- Archive the change with `openspec archive rate-limit-per-pool` before the final commit, in the same PR.
- PR body: "Closes #59", assignee gjed.
