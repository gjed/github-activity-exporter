# Proposal

## Why

`orgs:` only works for organizations: discovery calls `GET /orgs/{org}/repos`, which returns 404 for a user account. With `orgs: - org: gjed` every cycle fails and `/readyz` stays 503 forever, so a personal account has to list every repo under `repos:` and silently misses new ones ([#80](https://github.com/gjed/github-activity-exporter/issues/80)).

## What Changes

- Discovery detects whether each `orgs[].org` entry is an organization or a user account and lists its repos with the matching endpoint.
- User account, PAT owned by that user: private repos are included.
- User account, PAT of another identity: public repos only (GitHub exposes nothing more).
- User account, GitHub App installed on it: the repos the installation can access.
- `include_repos`, `exclude_repos`, `include_archived` and fork exclusion apply unchanged.
- An owner that does not exist fails discovery with an error naming the target and the owner.
- README: `orgs:` accepts organizations and user accounts; the visibility per auth mode is documented. No config key is renamed.

## Capabilities

### New Capabilities

### Modified Capabilities

- `repo-discovery`: autodiscovery accepts user accounts as owners, with owner-type detection and a clear error for unknown owners.

## Impact

- `internal/discovery/discoverer.go`: owner-type detection, user and installation listing; `New` takes the go-github client instead of the org-only `RepoLister`.
- `cmd/github-activity-exporter/main.go`: passes the REST client to discovery.
- `internal/discovery/discoverer_test.go`, `tests/e2e/mockgithub_test.go` (mock answers `GET /users/{name}`).
- `README.md`.
- One extra `GET /users/{name}` per owner per process lifetime, plus one `GET /user` per PAT target that has a user-account owner.
