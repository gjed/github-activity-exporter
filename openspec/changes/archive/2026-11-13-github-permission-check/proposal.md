# Proposal

## Why

Startup only proves that a target's credential authenticates (`Ping`: `GET /user` for a PAT, `GET /app` for a GitHub App), see [#23](https://github.com/gjed/github-activity-exporter/issues/23). A credential missing a permission passes startup and fails later: per-repo GraphQL errors mid-cycle, or silently empty results, since private repos the token cannot see never show up in discovery. The required permissions are not written down anywhere, so users find them by trial and error.

## What Changes

- The `github-auth` spec lists the minimum permissions per auth mode (GitHub App, classic PAT, fine-grained PAT) for every GitHub call the exporter makes on `main`: the owner lookup `GET /users/{owner}`, repo discovery (`GET /orgs/{org}/repos`, `GET /user` + `GET /user/repos?affiliation=owner`, `GET /users/{user}/repos`, `GET /installation/repositories`), the per-repo GraphQL PR scan, the batched GraphQL issue counts and the Dependency Dashboard lookup. The required set is the union over the collectors the target enables.
- A permission check runs per target at startup, after the existing credential check and before the first collection cycle:
  - GitHub App: reads the granted permissions and the repository selection from the installation token the exporter already exchanges, no extra request.
  - Classic PAT (and any token GitHub answers with `X-OAuth-Scopes`): reads the scopes from `GET /user`.
  - Fine-grained PAT (no `X-OAuth-Scopes`): permissions cannot be introspected, so it probes the GraphQL fields the enabled collectors read on one private repo per owner the token can see.
- Probes go through the target's GitHub client and transport: they wait on a paused rate-limit pool and on a secondary-limit pause, record only rate-limit state of the credential's own pools, and take no collection concurrency slot.
- Missing permissions fail startup with one error naming the target and every missing permission. The exporter exits before `/readyz` can report ready; `/readyz` and `/healthz` semantics are unchanged.
- README and chart README: the per-mode permission table and a GitHub App manifest example for organizations (Metadata, Pull requests, Issues read-only; no webhook).

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `github-auth`: required permissions per auth mode, and the startup permission check.

## Impact

- `internal/github`: App authenticator keeps the permissions and repository selection of the token exchange response; new permission check on `Client`.
- `cmd/github-activity-exporter/main.go`: runs the check after `Ping`, before `CredentialChecked`.
- `README.md`, `charts/github-activity-exporter/README.md`.
- No change to config, metrics, `/readyz` or `/healthz`.
