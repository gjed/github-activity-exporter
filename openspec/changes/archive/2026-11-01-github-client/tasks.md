## 1. Project Scaffolding

- [x] 1.1 Initialize Go module: `go mod init github.com/gjed/renovate-exporter`
- [x] 1.2 Create directory structure: `cmd/exporter/`, `internal/config/`, `internal/github/`, `internal/discovery/`, `internal/semconv/` (placeholder), `internal/exporter/`
- [x] 1.3 Add dependencies: `google/go-github/v62`, `shurcooL/githubv4`, `golang-jwt/jwt/v5`, `spf13/cobra`, `spf13/viper`
- [x] 1.4 Create `Makefile` with `build`, `test`, `lint` targets (no generate yet — that's observability-contract)
- [x] 1.5 Create minimal `cmd/exporter/main.go` with cobra root command and `--config` flag

## 2. Configuration Structs

- [x] 2.1 Define `Config`, `Target`, `Auth`, `AppAuth`, `OrgConfig` structs in `internal/config/`
- [x] 2.2 Implement YAML config loading with `viper`
- [x] 2.3 Implement env var overrides for `auth.token_env` and `auth.app.private_key_env`
- [x] 2.4 Add config validation: mutual exclusion of PAT vs App auth, required fields
- [x] 2.5 Write unit tests for config loading (table-driven, using fixture YAML files)

## 3. GitHub Auth

- [x] 3.1 Implement `internal/github/auth.go`: `Authenticator` interface with `Token(ctx) (string, error)` method
- [x] 3.2 Implement `PATAuthenticator`: returns the configured token directly
- [x] 3.3 Implement `AppAuthenticator`: JWT generation (RS256 with `golang-jwt/jwt`), REST call to exchange for installation token, token caching with TTL
- [x] 3.4 Implement auto-refresh: if cached token expires within 5 minutes, refresh before returning
- [x] 3.5 Implement credential validation: `Ping(ctx) error` calls `GET /user` (PAT) or `GET /app` (App) and returns a descriptive error on failure
- [x] 3.6 Wire `Ping()` into application startup; fatal exit with clear message on failure
- [x] 3.7 Write unit tests for both authenticators using mock HTTP server

## 4. GitHub Client

- [x] 4.1 Implement `internal/github/client.go`: wraps REST (`go-github`) and GraphQL (`githubv4`) clients; accepts an `Authenticator`
- [x] 4.2 Implement rate limit tracking: read `X-RateLimit-Remaining` from REST responses; parse GraphQL cost fields
- [x] 4.3 Implement pause-on-threshold logic: block new requests when remaining < 200, resume after reset time
- [x] 4.4 Expose rate limit state via hooks for self-metrics (callback or channel — wired up in observability-contract)
- [x] 4.5 Write unit tests for rate limit tracking and pause logic using mock HTTP server

## 5. Repository Discovery

- [x] 5.1 Implement `internal/discovery/discoverer.go`: `Discoverer` with `Repos(ctx) ([]Repo, error)` method
- [x] 5.2 Implement org repo listing: paginated `GET /orgs/{org}/repos?type=all`, filter archived/fork by default
- [x] 5.3 Implement glob include filter using `filepath.Match`
- [x] 5.4 Implement glob exclude filter; apply after include
- [x] 5.5 Implement explicit repo list mode (bypasses API listing)
- [x] 5.6 Implement multi-org: iterate multiple orgs, deduplicate results
- [x] 5.7 Implement incremental refresh: background goroutine updates repo list every `refresh_interval`
- [x] 5.8 Log discovered repos at `info` on startup and each refresh
- [x] 5.9 Write unit tests: autodiscovery, glob filters, explicit list, multi-org dedup (mock API server)
