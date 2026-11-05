# Design

## Context

`buildTargetRunner` in `cmd/github-activity-exporter/main.go` builds one authenticator and one `github.Client` per target, both hard-wired to `api.github.com`. The pieces for an override already exist but are unused in production:

- `PATAuthenticator` has `NewPATAuthenticatorWithBase`; `AppAuthOptions.BaseURL` feeds `/app` (ping) and `/app/installations/{id}/access_tokens`.
- `github.WithBaseURL` switches the client to `WithEnterpriseURLs(base+"/")` for REST and `base+"/graphql"` for GraphQL.

Two problems with the existing client path: GHES serves GraphQL at `/api/graphql`, not `/api/v3/graphql`; and go-github's `WithEnterpriseURLs` appends `api/v3/` to any URL that does not already end in it, so the configured value is not used verbatim.

## Goals / Non-Goals

**Goals:**

- One per-target value drives REST, GraphQL, token exchange and ping.
- Default behaviour against github.com unchanged.

**Non-Goals:**

- GHES upload URL, custom CA bundles, proxies.
- Accepting a bare GHES host (`https://ghes.example.com`) and guessing `/api/v3`.

## Decisions

- **`base_url` is the REST API root**, the same value `gh`, octokit and go-github call the base URL. Auth endpoints (`/user`, `/app`, `/app/installations/...`) live under it on both github.com and GHES, so the authenticators use it unchanged. Alternative: a host-only setting with derived paths. Rejected: it hides the actual URL and breaks for proxies or non-standard paths.
- **GraphQL URL derivation:** default base → `https://api.github.com/graphql`; base path ending in `/api/v3` → replace with `/api/graphql`; otherwise `<base>/graphql`. Alternative: a second `graphql_url` key. Rejected: the issue asks for one setting, and the derivation covers both real deployments.
- **REST client uses the base verbatim.** Set the go-github client's `BaseURL` to `<base>/` directly instead of `WithEnterpriseURLs`, so no `api/v3/` is appended behind the user's back. One code path for default and custom URLs.
- **Default and normalisation in `config.Load`.** Empty `base_url` becomes `https://api.github.com`; a trailing `/` is trimmed; validation requires scheme `http`/`https` and a non-empty host. Downstream code receives a clean value.
- **Isolation test without real network.** Tests point each target at its own `httptest` server and wrap `http.DefaultTransport` (used by both the client and the authenticators) with a guard that fails on any non-loopback host, so a request leaking to `api.github.com` fails the test instead of hitting the network.

## Risks / Trade-offs

- \[A user sets `base_url: https://ghes.example.com` without `/api/v3`\] → Requests 404 at the startup credential check, which names the target. README shows the `/api/v3` form.
- \[Swapping `http.DefaultTransport` in tests is global\] → Restored with `t.Cleanup`; the tests in that package do not run in parallel.
