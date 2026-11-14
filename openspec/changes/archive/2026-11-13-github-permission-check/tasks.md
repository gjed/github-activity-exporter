# Tasks

## 1. GitHub client

- [x] 1.1 `AppAuthenticator`: keep `permissions` and `repository_selection` from the installation token response; accessor that ensures a token first
- [x] 1.2 `internal/github/permissions.go`: required set from the enabled collectors; checks per auth mode (App grants, classic `X-OAuth-Scopes` via `GET /user`, fine-grained GraphQL probe per owner); one error naming the target and every missing permission
- [x] 1.3 Unit tests with `httptest`: each mode passing and missing one and several permissions; write satisfies read; no private repo passes; probe waits on a secondary-limit pause; no JWT request

## 2. Startup wiring

- [x] 2.1 `buildTargetRunner`: run the check after `Ping`, before `CredentialChecked`; log App repository selection
- [x] 2.2 `main_test.go` coverage for the failing check keeping the target not ready

## 3. Docs

- [x] 3.1 README: permission table per mode, App manifest example for orgs, relation to `/readyz` and `/healthz`
- [x] 3.2 Chart README: same table and manifest example
- [x] 3.3 Markdown lint on changed `.md` files

## 4. Validation

- [x] 4.1 `go build ./...`, `go test ./...`, `make check-schema`, `make generate` with no diff
- [x] 4.2 Real run: `gh auth token` against `gjed` passes
- [x] 4.3 Real run: token missing a required permission fails fast naming it
- [x] 4.4 `openspec archive github-permission-check` before the final commit
