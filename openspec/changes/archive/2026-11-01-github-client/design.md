## Context

The GitHub client layer is the foundation everything else builds on. Two API surfaces are needed: REST v3 for org/repo listing and issue search, GraphQL v4 for efficient PR data fetching (multiple fields in one request). Both share the same authentication layer and rate limit tracking per target.

## Goals / Non-Goals

**Goals:**

- PAT and GitHub App auth, per-target, credential-isolated
- GitHub App token auto-refresh (installation tokens expire after 1h)
- Adaptive rate limit handling: pause at configurable threshold, resume after reset
- Org autodiscovery: list repos in one or more orgs, with glob include/exclude filters
- Explicit repo list mode as an alternative to autodiscovery
- Multi-org: each target can have multiple orgs
- Incremental repo list refresh (without restart)
- Rate limit state exposed as metrics (hooks into `observability-contract` self-metrics)

**Non-Goals:**

- Writing to GitHub (no PR creation, no issue updates)
- Cross-org App installations (each org installation is configured explicitly)
- Caching of PR/issue data — that belongs in the collectors

## Decisions

### Decision: `internal/github/` wraps go-github and githubv4, not used directly in collectors

Rationale: Keeps the GitHub API surface behind an interface. Collectors call `client.ListPRs(ctx, repo)` — they never import `go-github` directly. This makes collectors testable with mock clients without hitting the network.

### Decision: GitHub App JWT signed with RS256 using `golang-jwt/jwt`

Rationale: GitHub App authentication requires a JWT signed with the App's private key (RS256). `golang-jwt/jwt` is the canonical Go JWT library. The JWT is exchanged for an installation token via REST; the installation token is cached and refreshed 5 minutes before expiry.

### Decision: Rate limit tracked per target, not globally

Rationale: Each target has its own credentials and therefore its own rate limit quota. Tracking per-target avoids false positives when one target is throttled but another is not.

### Decision: Glob patterns via `path/filepath.Match` (no external dependency)

Rationale: Standard library glob is sufficient for `*-i18n`, `archived-*` patterns. No need for a full regex engine for repo name filtering.

### Decision: REST for org repo listing, GraphQL v4 for PR queries

Rationale: Org repo listing (`/orgs/{org}/repos`) is straightforward REST with pagination. GraphQL v4 allows fetching all PR fields (state, labels, createdAt, mergedAt, closedAt, reviewDecisions) in a single paginated query, dramatically reducing API call count vs. per-field REST requests.

## Risks / Trade-offs

- **GraphQL v4 rate limit uses "points" not requests** → Mitigation: check both `X-RateLimit-Remaining` (REST) and GraphQL cost response fields; treat either hitting threshold as a pause trigger
- **GitHub App private key on disk** → Mitigation: support `private_key_path` (file) and `private_key_env` (base64-encoded env var); document rotation procedure
- **Glob filter false negatives** → Mitigation: log which repos are included/excluded at startup at `debug` level; easy to audit

## Open Questions

- Should the client expose a `Ping()` method to verify credentials on startup? → Yes, fail fast with a clear error message rather than discovering bad credentials on first collection cycle
