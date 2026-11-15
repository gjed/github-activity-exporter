# Configuration

The exporter takes no flags other than `--version`. Targets, GitHub auth and export settings come from a YAML file.

## Config file

The path is read from `CONFIG_FILE`. It defaults to `config.yaml` in the working directory, and to `/etc/github-activity-exporter/config.yaml` in the Docker image.

```yaml
collection_interval: 5m           # period between collection cycle starts, see collection.md
collection:
  concurrency: 4                  # default for every target: repos or batches collected at once, 1 to 100; see collection.md
  pr_author: false                # default for every target: add github.pr.author to the PR histograms; see metrics.md
export:
  interval: 60s                 # OTLP push period, independent of collection_interval; see collection.md
  otlp:
    endpoint: https://otlp.example.com:4318  # required; http: full URL, grpc: host:port without scheme
    protocol: http                # http or grpc
    headers:                      # sent on every OTLP request; names are lower-cased
      Authorization: Bearer <token>
      X-Scope-OrgID: my-tenant
    headers_env: ""               # env var holding more headers as name=value pairs; see below
    tls:
      insecure: false             # http: skip cert verification; grpc: no TLS. Not for production
  prometheus:
    enabled: false                # optional /metrics bridge
    listen_address: ":9090"
health:                           # /healthz and /readyz, always on; see health.md
  listen_address: ":10062"        # must differ from export.prometheus.listen_address when the bridge is enabled
  stall_timeout: 0s               # 0 means 2 × collection_interval + 1h; otherwise must exceed collection_interval
  unauthorized_cycles: 3          # consecutive cycles answered only with 401 before /healthz fails
telemetry:                        # self-telemetry, see self-telemetry.md
  enabled: false
targets:
  - name: my-org                  # unique per target
    base_url: https://api.github.com  # REST API root; GHES: https://<host>/api/v3
    auth:                         # exactly one of token, token_env, or app; empty or omitted uses GITHUB_TOKEN
      token_env: MY_GITHUB_TOKEN  # env var holding a PAT with repo read access
      # app:
      #   app_id: 12345
      #   installation_id: 67890
      #   private_key_path: /etc/github-activity-exporter/key.pem
      #   private_key_env: GITHUB_APP_KEY   # base64-encoded PEM, instead of the path
    orgs:                         # at least one org or repo is required
      - org: my-org               # organization or user account
        include_repos: []
        exclude_repos: []
        include_archived: false
    repos:
      - other-owner/some-repo
    discovery:
      refresh_interval: 60m       # re-list orgs to pick up new and removed repos; a failed refresh keeps the last list; unused when repos is set
    filters:
      prs:                        # a PR is selected only if it passes every non-empty filter
        authors: []               # author logins, case-insensitive; GitHub App bots as "renovate[bot]"
        head_branch_prefix: ""    # head branch must start with this, case-sensitive
        include_labels: []        # at least one of these labels
        exclude_labels: []        # none of these labels
        states: []                # open, closed, draft, merged (current state)
      issues:
        exclude_title_patterns: [] # regular expressions
        states: []                # open, closed
    renovate_dashboard:
      enabled: false              # collect the Renovate Dependency Dashboard issue
    graphql_batch_size: 20        # repos per issue count and Dependency Dashboard query, 1 to 100; see collection.md
    collection:
      concurrency: 4              # overrides the top-level collection.concurrency for this target
      pr_author: false            # overrides the top-level collection.pr_author for this target
```

Quote logins that contain brackets in flow lists: `authors: ["renovate[bot]"]`. Unquoted, YAML reads `[bot]` as a nested list.

`authors` matches the REST login of a GitHub App bot (`renovate[bot]`), but the `github.pr.author` attribute records the GraphQL login (`renovate`); see [PR size and author](metrics.md#pr-size-and-author).

`orgs:` accepts organizations and user accounts; the exporter looks up the account type once per process. Which repos of a user account are discovered depends on the auth:

| Auth                                | Repos discovered         |
| ----------------------------------- | ------------------------ |
| PAT owned by that user              | public and private       |
| PAT of another user                 | public only              |
| GitHub App installed on the account | the installation's repos |

`include_repos`, `exclude_repos` and `include_archived` apply to user accounts as to organizations. An `orgs` entry that names no existing account fails discovery with an error naming the target and the owner.

## Renovate preset

To track only Renovate PRs and the Dependency Dashboard, select Renovate PRs by author (or by the `renovate` label) and enable the dashboard collector:

```yaml
targets:
  - name: renovate
    orgs:
      - org: my-org
    filters:
      prs:
        authors: ["renovate[bot]"]  # self-hosted Renovate: its user login
        # include_labels: [renovate]  # alternative: Renovate's `labels` setting
    renovate_dashboard:
      enabled: true
```

`renovate.*` attributes appear only on the dashboard metrics. A target without `renovate_dashboard.enabled: true` never requests the dashboard issue.

## Environment variables

| Env                  | Default         | Description                                                                                               |
| -------------------- | --------------- | --------------------------------------------------------------------------------------------------------- |
| `CONFIG_FILE`        | `config.yaml`   | Path to the config file                                                                                   |
| `GITHUB_TOKEN`       |                 | PAT for targets with an empty or omitted `auth` block                                                     |
| `RENOVATE_BOT_LOGIN` | `renovate[bot]` | Login of the Renovate bot that owns the Dependency Dashboard, in REST form: `<app>[bot]` for a GitHub App |

A target's credential is resolved in this order:

1. The target's own `auth.token`, `auth.token_env` or `auth.app`. `GITHUB_TOKEN` is then ignored for that target.
1. `GITHUB_TOKEN`, when the target's `auth` block is empty or omitted. Every such target shares it. If it is unset or empty, startup fails with an error naming the target.

The permissions each credential needs, and the startup check of them, are in [GitHub permissions](github-permissions.md).

A partially filled `auth` block (for example `app` without `app_id`) is a startup error; it does not fall back to `GITHUB_TOKEN`.

`base_url` drives every GitHub call for the target: REST, GraphQL, the GitHub App token exchange and the startup credential check. GraphQL goes to `https://api.github.com/graphql` by default and to `https://<host>/api/graphql` when `base_url` ends in `/api/v3`. Targets with different `base_url` values can run in one deployment.

Scalar config keys can be overridden with `GITHUB_ACTIVITY_EXPORTER_` env vars, upper-cased with `.` replaced by `_` (for example `GITHUB_ACTIVITY_EXPORTER_EXPORT_OTLP_ENDPOINT`). Per-target settings, including `auth`, cannot be overridden this way, and neither can the `export.otlp.headers` map: use `export.otlp.headers_env`.

## OTLP headers from the environment

`export.otlp.headers_env` names an env var holding OTLP headers, so a token can come from a Secret instead of the config file. The format is the one of `OTEL_EXPORTER_OTLP_HEADERS`: `name=value` pairs separated by commas, for example `authorization=Bearer <token>,x-scope-orgid=my-tenant`. Names are lower-cased; values are trimmed and percent-decoded, so write a `,` in a value as `%2C` and a `%` as `%25`.

The headers are merged into `export.otlp.headers`; for the same name the env var wins, so non-secret headers can stay in the file. If the variable is unset or empty, or an entry is not a valid `name=value` pair or its value holds a control character such as a newline, startup fails with an error naming the variable and the entry position. Header values are never logged.

`OTEL_EXPORTER_OTLP_HEADERS` does not reach the GitHub metrics push: it configures [self-telemetry](self-telemetry.md) only.

The first time an OTLP push is rejected with HTTP 401 or 403, or gRPC `Unauthenticated` or `PermissionDenied`, the exporter logs a warning pointing at these settings. Every failed push is logged and counted in `github_exporter.otlp.errors`; `/healthz` and `/readyz` do not depend on OTLP pushes.
