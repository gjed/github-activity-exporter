# Tasks

## 1. Config

- [ ] 1.1 Add `Target.BaseURL` (`base_url`) in `internal/config/config.go`: default `https://api.github.com`, trim trailing `/`, reject non-`http(s)` or host-less values with an error naming the target; verify `go build ./...`
- [ ] 1.2 Add config tests and testdata: omitted `base_url` gets the default; two targets keep distinct custom values with trailing `/` trimmed; invalid `base_url` fails naming the target; verify `go test ./internal/config/...`

## 2. GitHub client and wiring

- [ ] 2.1 In `internal/github/client.go`, set the REST `BaseURL` to `<base>/` verbatim and derive the GraphQL URL (default, `/api/v3` → `/api/graphql`, else `/graphql`); update the base URL doc comments in `auth.go`
- [ ] 2.2 In `cmd/github-activity-exporter/main.go`, pass `t.BaseURL` to the PAT and App authenticators and to `NewClient` via `WithBaseURL`; verify `go build ./...`
- [ ] 2.3 Add httptest tests in `internal/github`: with a GHES-shaped base URL, REST, GraphQL, PAT ping, App ping and installation token exchange all reach the test server at the expected paths and no request goes to a non-loopback host; two clients with different base URLs each reach only their own server; verify `go test ./internal/github/...`

## 3. Documentation

- [ ] 3.1 Document `base_url` in the `README.md` config reference with the GHES form; verify markdown lint passes on `README.md`

## 4. Integration

- [ ] 4.1 Run `go build ./... && go test ./...`; verify both pass
