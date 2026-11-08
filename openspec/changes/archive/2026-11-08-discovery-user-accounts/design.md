# Design

## Context

`Discoverer.listOrgRepos` pages `Repositories.ListByOrg` (`GET /orgs/{org}/repos?type=all`). The `Discoverer` gets an org-only `RepoLister` and the `config.Target`, which carries the auth mode: `Auth.App.AppID != 0` is a GitHub App installation, otherwise `Auth.Token` holds a PAT (config validation enforces exactly one).

## Goals / Non-Goals

**Goals:**

- Discover repos of user accounts listed under `orgs:` for PAT and App auth.
- Keep the org path and its request pattern unchanged apart from one cached type lookup.

**Non-Goals:**

- A separate `users:` key or any config rename.
- Repos a user can access but does not own (`affiliation=collaborator,organization_member`).

## Decisions

### Detect the owner type up front, cache it

`GET /users/{name}` returns `type: "User"` or `"Organization"`. It is called once per owner and cached for the process lifetime in the `Discoverer` (owner type cannot change without a rename, which would change the config anyway). A failed lookup is not cached, so the next refresh retries.

Alternative: try `GET /orgs/{org}/repos` and fall back on 404. Rejected: a 404 also means "org exists but the token cannot see it" on some setups, the fallback hides typos behind a second request, and it costs a failed call every refresh for every user owner. The explicit lookup costs one call per owner per process.

A 404 on the lookup is the "owner not found" error: `target "<t>": owner "<o>" not found (GET /users/<o> returned 404)`.

### Endpoint per owner type and auth mode

| Owner        | Auth                    | Endpoint                                         | Visibility          |
| ------------ | ----------------------- | ------------------------------------------------ | ------------------- |
| Organization | PAT or App              | `GET /orgs/{org}/repos?type=all` (unchanged)     | what the token sees |
| User         | PAT owned by that user  | `GET /user/repos?affiliation=owner`              | public and private  |
| User         | PAT of another identity | `GET /users/{user}/repos?type=owner`             | public only         |
| User         | App installation        | `GET /installation/repositories`, owner filtered | installation repos  |

- "PAT owned by that user" compares the user name with the login from `GET /user`, case-insensitively. The login is fetched once per `Discoverer` and cached; it is only requested when a user owner exists.
- Installation tokens cannot call `GET /user` or `/user/repos` (403), and `GET /users/{user}/repos` is public-only, so App targets use `/installation/repositories` and keep the repos whose `owner.login` equals the user. An installation on another account yields zero repos for that owner; discovery logs a warning instead of failing, matching an org whose filters exclude everything.
- All endpoints page with `per_page=100`. Archived, fork, include and exclude filtering runs on the merged list exactly as for orgs.

### Pass the go-github client to discovery

The five calls span three go-github services (`Repositories`, `Users`, `Apps`). `discovery.New` takes `*github.Client` instead of growing `RepoLister` into a five-method interface plus an adapter in `main`. Existing tests already build a real client against `httptest`, so they only change the argument.

## Risks / Trade-offs

- [A fine-grained PAT restricted to some repos] → `/user/repos` returns only those; same behavior as orgs today.
- [App installed on a different account than the configured user] → zero repos plus a warning naming target and owner, rather than a hard failure.
- [e2e mock answers unknown paths with 404 and fails the test] → the mock gains a `GET /users/{name}` handler returning `type: Organization`.
