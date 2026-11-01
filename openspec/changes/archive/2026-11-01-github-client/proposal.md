## Why

All metric collection depends on a reliable, authenticated GitHub API client. This change delivers the foundational layer: PAT and GitHub App authentication, org/repo autodiscovery with filtering, and rate-limit-aware request handling. Without this, data-collectors cannot be implemented. It is self-contained and fully testable in isolation with mock HTTP servers.

## What Changes

- `internal/github/` package: authenticated HTTP client with PAT and GitHub App support
- `internal/discovery/` package: org autodiscovery, include/exclude glob filters, explicit repo list, multi-org, incremental refresh
- Rate limit awareness: pause when remaining requests drop below threshold; expose rate limit metrics

## Capabilities

### New Capabilities

- `github-auth`: PAT authentication, GitHub App JWT + installation token, auto-refresh, per-target credential isolation, rate limit metrics
- `repo-discovery`: org autodiscovery, include/exclude glob filters, explicit repo list, multi-org, archived/fork exclusion, incremental refresh

### Modified Capabilities

## Impact

- New packages: `internal/github/`, `internal/discovery/`
- Dependencies: `google/go-github` (REST v3), `shurcooL/githubv4` (GraphQL v4), `golang-jwt/jwt` (GitHub App JWT signing)
- Parallel with: `observability-contract`, `ci-pipeline`
- Required by: `data-collectors`
