# GitHub permissions

A target's credential needs read access only. Every GitHub App installation and fine-grained PAT has Metadata; public repos need no permission with any credential.

| GitHub call                                        | Used by                                                       | GitHub App          | Classic PAT              | Fine-grained PAT    |
| -------------------------------------------------- | ------------------------------------------------------------- | ------------------- | ------------------------ | ------------------- |
| `GET /users/{owner}`                               | owner type lookup, every `orgs` entry                         | none                | none                     | none                |
| `GET /orgs/{org}/repos`                            | discovery, organization                                       | Metadata: read      | `repo` for private repos | Metadata: read      |
| `GET /user`, `GET /user/repos?affiliation=owner`   | discovery, user account owning the PAT                        | n/a                 | `repo` for private repos | Metadata: read      |
| `GET /users/{user}/repos`                          | discovery, user account not owning the PAT (public only)      | n/a                 | none                     | none                |
| `GET /installation/repositories`                   | discovery, user account with the App installed                | Metadata: read      | n/a                      | n/a                 |
| GraphQL `repository.pullRequests`                  | PR collector                                                  | Pull requests: read | `repo`                   | Pull requests: read |
| GraphQL `repository.issues`                        | issue collector                                               | Issues: read        | `repo`                   | Issues: read        |
| GraphQL `repository.issues(filterBy: {createdBy})` | Dependency Dashboard collector (`renovate_dashboard.enabled`) | Issues: read        | `repo`                   | Issues: read        |

The PR and issue collectors always run, so the minimum is Metadata, Pull requests and Issues read-only for a GitHub App or a fine-grained PAT, and the `repo` scope for a classic PAT. `read:org` is not needed. The Dependency Dashboard collector adds no permission.

At startup, after the credential check and before the first collection cycle, the exporter checks each target's permissions and exits with one error naming the target and every missing permission:

- GitHub App: the permissions and repository selection the installation token comes with. No extra request. With repository selection `selected`, only the selected repos are discovered.
- Classic PAT or OAuth token (GitHub answers it with `X-OAuth-Scopes`): `repo` must be among the scopes from `GET /user`. A token without `repo` sees no private repo, so it fails even for an owner that has none; use a fine-grained PAT for public repos only.
- Fine-grained PAT: scopes cannot be read, so one GraphQL query per owner reads the collectors' fields on one private repo the token can see. Every token reads public repos, so an owner with no private repo visible to the token passes. The check cannot tell that from a token not granted access to the owner's private repos: a fine-grained PAT reads private repos of its resource owner only.

The check's requests go through the target's GitHub client: they wait on a paused rate-limit pool or a secondary rate limit pause, and take no `collection.concurrency` slot. Until the check passes, [`/readyz`](health.md) reports the target's credential check as pending; a failed check exits the exporter, so it never reports ready. `/healthz` is not affected: the check runs before the first cycle, and 401 counting starts with it.

A GitHub App for an organization, registered from this [manifest](https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest) by posting it as the `manifest` form field to `https://github.com/organizations/<org>/settings/apps/new`. It has no webhook: no `hook_attributes` and no `default_events`.

```json
{
  "name": "github-activity-exporter",
  "url": "https://github.com/gjed/github-activity-exporter",
  "public": false,
  "default_permissions": {
    "metadata": "read",
    "pull_requests": "read",
    "issues": "read"
  },
  "default_events": []
}
```

Install the App on the organization, then set `auth.app.app_id`, `auth.app.installation_id` and the private key on the target.
