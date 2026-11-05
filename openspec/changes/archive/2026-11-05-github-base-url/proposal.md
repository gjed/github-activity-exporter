# Proposal

## Why

The exporter only talks to `api.github.com`, so GitHub Enterprise Server users cannot run it. `AppAuthOptions.BaseURL` exists but only tests set it, and the client's `WithBaseURL` option is never wired from config. [Issue #22](https://github.com/gjed/github-activity-exporter/issues/22).

## What Changes

- New per-target config key `base_url`, the REST API root. Default `https://api.github.com`; for GHES `https://<host>/api/v3`.
- That one value drives every outbound call for the target: REST, GraphQL, the GitHub App JWT/installation token exchange, and the startup credential check.
- GraphQL URL is derived from `base_url`: `https://api.github.com/graphql` for the default, `https://<host>/api/graphql` when `base_url` ends in `/api/v3`, otherwise `<base_url>/graphql`.
- Fix the existing custom-URL path in the client, which builds `<base>/graphql` (wrong for GHES, which serves `/api/graphql`).
- `base_url` must be an `http(s)` URL with a host; a trailing `/` is trimmed. Anything else is a startup error naming the target.
- Update the README config reference and the `github-auth` spec.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `github-auth`: adds a per-target API base URL requirement covering REST, GraphQL, token exchange and the credential check, with isolation between targets.

## Impact

- `internal/config/config.go`: `Target.BaseURL`, default, normalisation, validation.
- `internal/github/client.go`: REST and GraphQL endpoints from the base URL.
- `internal/github/auth.go`: doc comments only; the base URL plumbing already exists.
- `cmd/github-activity-exporter/main.go`: pass the target's base URL to the authenticator and the client.
- Tests: config cases; httptest-backed tests proving a custom base URL never reaches `api.github.com` and two targets with different base URLs stay isolated.
- `README.md`: config reference. The repo has no standalone example config file; the README block is the example config.
- Out of scope: GHES upload URLs (the exporter never uploads), per-target TLS/CA settings, proxy settings.
