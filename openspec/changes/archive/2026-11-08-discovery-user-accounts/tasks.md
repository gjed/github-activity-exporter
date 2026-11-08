# Tasks

## 1. Discovery

- [x] 1.1 Replace `RepoLister` with `*github.Client` in `discovery.New` and update `main.go` and existing tests; verify `go build ./...` and `go test ./internal/discovery/...` pass unchanged
- [x] 1.2 Add cached owner-type lookup (`GET /users/{name}`) with the not-found error naming target and owner; verify with httptest tests for an org owner, a 404 owner, and a single lookup across refreshes
- [x] 1.3 Add user listing per auth mode (PAT self via `/user/repos?affiliation=owner`, PAT other via `/users/{user}/repos`, App via `/installation/repositories` filtered by owner) with shared archived/fork/include/exclude filtering; verify with httptest tests for each mode, including private repos for PAT self and filters on a user account
- [x] 1.4 Answer `GET /users/{name}` in the e2e GitHub mock; verify `go test ./tests/e2e/...` passes

## 2. Docs

- [x] 2.1 Document in `README.md` that `orgs:` accepts organizations and user accounts, with repo visibility per auth mode; verify markdown lint passes on the file

## 3. Verification

- [x] 3.1 Run `go build ./...` and `go test ./...`; run the binary against api.github.com with `orgs: - org: gjed` and `GITHUB_TOKEN=$(gh auth token)`, verify the discovery log lists gjed's private repos and `/readyz` returns 200

## Workflow follow-up

- Archive the change with `openspec archive discovery-user-accounts` before the final commit.
