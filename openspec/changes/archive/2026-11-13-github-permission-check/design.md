# Design

## Context

Startup builds one GitHub client per target (`cmd/github-activity-exporter/main.go` `buildTargetRunner`), calls `Authenticator.Ping`, then `tracker.CredentialChecked`. Any error there exits the process, so `/readyz` never reports ready. Ping uses the authenticator's HTTP client (base transport only); collection uses `Client`, whose `rateLimitTransport` waits on paused pools and on the target-wide secondary-limit pause, and feeds the rate-limit gauges.

GitHub calls on `main`:

| Call                                                      | Where                            | Used when                                |
| --------------------------------------------------------- | -------------------------------- | ---------------------------------------- |
| `GET /users/{owner}`                                      | `discovery.ownerType`            | every `orgs` entry, cached per process   |
| `GET /orgs/{org}/repos?type=all`                          | `discovery.listOrg`              | owner is an organization                 |
| `GET /user`, then `GET /user/repos?affiliation=owner`     | `discovery.patLogin`, `listUser` | PAT, owner is the token's user           |
| `GET /users/{user}/repos?type=owner`                      | `discovery.listUser`             | PAT, owner is another user (public only) |
| `GET /installation/repositories`                          | `discovery.listInstallation`     | App, owner is a user account             |
| GraphQL `repository.pullRequests` (+ `labels`, `reviews`) | `collector/pr.go`                | always                                   |
| GraphQL `repository.issues` counts, `labels.issues`       | `collector/issue.go`, `batch.go` | always                                   |
| GraphQL `repository.issues(filterBy: {createdBy})`        | `collector/dashboard.go`         | `renovate_dashboard.enabled`             |

Explicit `repos` targets skip discovery.

## Permissions per auth mode

| Call                                       | GitHub App          | Classic PAT                       | Fine-grained PAT    |
| ------------------------------------------ | ------------------- | --------------------------------- | ------------------- |
| `GET /users/{owner}`, `GET /user`          | none                | none                              | none                |
| Repo listing (org, own user, installation) | Metadata: read      | `repo` for private/internal repos | Metadata: read      |
| `GET /users/{user}/repos`                  | n/a                 | none (public only)                | none                |
| GraphQL PR scan                            | Pull requests: read | `repo`                            | Pull requests: read |
| GraphQL issue counts                       | Issues: read        | `repo`                            | Issues: read        |
| GraphQL Dependency Dashboard               | Issues: read        | `repo`                            | Issues: read        |

`read:org` is not needed: org repo listing and GraphQL repository reads need only `repo`. Metadata is granted to every App installation and fine-grained PAT, so the only permissions the check can find missing are Pull requests and Issues (App, fine-grained) and `repo` (classic). The dashboard collector adds no permission beyond the issue collector, which is always on; the check still builds the required set from the enabled collectors so a future collector adds its own.

## Decisions

### App: permissions from the token exchange

`POST /app/installations/{id}/access_tokens` answers with `permissions` and `repository_selection` next to the token. `AppAuthenticator.fetchInstallationToken` already decodes that body; it keeps the two fields. The check calls `Token` (the first exchange happens there, or reuses the cached one) and reads them. No extra request, and no JWT request through the target's rate-limit transport, whose headers would describe the App's own quota and skew the installation's `core` gauge. Alternative rejected: `GET /app/installations/{id}` with the JWT, an extra request against a different quota.

`repository_selection: selected` is logged at info with the target: discovery then lists only the selected repos, which is the intended behaviour, not an error.

### Classic PAT: `X-OAuth-Scopes` from `GET /user`

The check sends `GET /user` through `Client.REST()`, so it goes through the rate-limit transport. Its `core` headers are the token's own and are recorded as any other request. A response with an `X-OAuth-Scopes` header is a classic PAT or OAuth token (`gh auth token` returns one); `repo` must be in the list. Scope detection uses the header, not the token prefix, so GHES and OAuth tokens behave the same.

### Fine-grained PAT: GraphQL probe per owner

No `X-OAuth-Scopes` header means scopes cannot be read. Every fine-grained PAT reads all public repos, so a missing permission only shows on a private repo it can see; its permissions are the same on all its repos. Per distinct owner (the `orgs` entries, or the owners of explicit `repos`), one query through `Client.QueryRaw`:

```graphql
query($owner: String!) {
  repositoryOwner(login: $owner) {
    repositories(first: 1, privacy: PRIVATE) {
      nodes {
        pullRequests(first: 1) { totalCount }
        issues(first: 1) { totalCount }
      }
    }
  }
}
```

The dashboard's `issues(filterBy: {createdBy: $login})` field is added when the collector is enabled. A GraphQL error of type `FORBIDDEN` whose path ends in a probed field maps to its permission. No private repo means nothing to check for that owner. Discovery's REST calls are not probed: they need only Metadata, which a fine-grained PAT always has. Other request errors fail startup, as Ping does today.

### Rate limits and concurrency

All probe requests go through `Client`'s HTTP client, so they wait on a paused pool and on the secondary-limit pause, and are retried like collection requests. They call the client directly, never the collector `Limiter`, so they take no concurrency slot. They run before the runner starts, so they never compete with collection.

### Error format

One error per target, listing every missing permission, e.g. `target "gjed": missing GitHub permissions: pull_requests: read, issues: read (fine-grained PAT; probed on gjed/private-repo)` or `target "gjed": classic token missing OAuth scope: repo (granted: read:org, workflow)`. `main` wraps it as today (`failed to build runners`).

### /readyz and /healthz

The check runs inside `buildTargetRunner`, between `Ping` and `CredentialChecked`. Until then `/readyz` reports the target's credential check as pending; a failure exits the process. `/healthz` is untouched: probe responses fall before the first `CycleStarted`, which resets the per-cycle 401 counters, so they never count towards the unauthorized-cycle threshold.

## Risks / Trade-offs

- A fine-grained PAT owned by one account can see only public repos of other owners. The check cannot tell missing repository access from an owner with no private repos; both pass. README states it.
- Startup grows by one request per classic PAT target and one GraphQL query per owner per fine-grained PAT target.
- A transient GitHub error during the probe fails startup, same as Ping today; the pod restarts.
- The `FORBIDDEN` mapping relies on GitHub's GraphQL error shape for fine-grained PATs; the e2e run with a real fine-grained token missing a permission verifies it.
