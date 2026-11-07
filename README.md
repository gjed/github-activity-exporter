# github-activity-exporter

[![CI](https://github.com/gjed/github-activity-exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/gjed/github-activity-exporter/actions/workflows/ci.yml)
[![Registry Validate](https://github.com/gjed/github-activity-exporter/actions/workflows/registry-validate.yml/badge.svg)](https://github.com/gjed/github-activity-exporter/actions/workflows/registry-validate.yml)
[![Release](https://github.com/gjed/github-activity-exporter/actions/workflows/release.yml/badge.svg)](https://github.com/gjed/github-activity-exporter/actions/workflows/release.yml)
[![Latest Release](https://img.shields.io/github/v/release/gjed/github-activity-exporter)](https://github.com/gjed/github-activity-exporter/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/gjed/github-activity-exporter)](go.mod)
[![License](https://img.shields.io/github/license/gjed/github-activity-exporter)](LICENSE)

GitHub pull request and issue activity exporter — collects PR and issue metrics from the GitHub API, optionally including [Renovate](https://docs.renovatebot.com/) Dependency Dashboard state, and pushes them via OTLP to any OpenTelemetry-compatible backend (Grafana Cloud, Prometheus with OTLP receiver, Datadog, etc.).

## Features

- Pushes PR and issue metrics via **OTLP** (HTTP or gRPC) as primary output
- Optional Prometheus `/metrics` bridge for scrape-based setups (disabled by default)
- Always-on `/healthz` and `/readyz` endpoints for liveness and readiness probes
- Metrics conform to the [OTel semantic convention registry](registry/) and are validated by [Weaver](https://github.com/open-telemetry/weaver) in CI
- Multi-arch Docker image published to [GHCR](https://ghcr.io/gjed/github-activity-exporter)
- Fully automated releases via [semantic-release](https://semantic-release.gitbook.io/) — no manual tagging

## Quick Start

Create `config.yaml`:

```yaml
export:
  otlp:
    endpoint: http://my-otel-collector:4318
targets:
  - name: my-org
    orgs:
      - org: my-org
```

With no `auth` block the target uses the `GITHUB_TOKEN` env var as a PAT.

Run the image with the config mounted at `/etc/github-activity-exporter/config.yaml`:

```bash
docker run --rm \
  -e GITHUB_TOKEN=ghp_... \
  -v "$PWD/config.yaml:/etc/github-activity-exporter/config.yaml:ro" \
  ghcr.io/gjed/github-activity-exporter:latest
```

Metrics are pushed to the OTLP endpoint after each collection cycle (default: every 5 minutes).

To also expose a Prometheus scrape endpoint, set `export.prometheus.enabled: true` in the config and add `-p 9090:9090`.

## Installation

### Docker

```bash
docker pull ghcr.io/gjed/github-activity-exporter:latest
```

### Kubernetes

The Helm chart is published to GHCR with each release; the chart version equals the exporter version:

```bash
helm install github-activity-exporter oci://ghcr.io/gjed/charts/github-activity-exporter \
  --version X.Y.Z --namespace github-activity-exporter --create-namespace -f values.yaml
```

`values.yaml` holds the exporter config file verbatim under `config`, and Secret wiring for the GitHub credentials and OTLP headers under `secret`. See the [chart README](charts/github-activity-exporter/README.md) for the values, the probe settings and why the chart runs a single replica, and the [examples](charts/github-activity-exporter/examples/).

### From source

```bash
go install github.com/gjed/github-activity-exporter/cmd/github-activity-exporter@latest
```

## Configuration

The exporter takes no flags other than `--version`. Targets, GitHub auth and export settings come from a YAML file.

### Config file

The path is read from `CONFIG_FILE`. It defaults to `config.yaml` in the working directory, and to `/etc/github-activity-exporter/config.yaml` in the Docker image.

```yaml
collection_interval: 5m           # how often to collect and push metrics
export:
  otlp:
    endpoint: https://otlp.example.com:4318  # required; http: full URL, grpc: host:port without scheme
    protocol: http                # http or grpc
    headers:                      # sent on every OTLP request; names are lower-cased
      Authorization: Bearer <token>
      X-Scope-OrgID: my-tenant
    tls:
      insecure: false             # http: skip cert verification; grpc: no TLS. Not for production
  prometheus:
    enabled: false                # optional /metrics bridge
    listen_address: ":9090"
health:                           # /healthz and /readyz, always on
  listen_address: ":10062"        # must differ from export.prometheus.listen_address when the bridge is enabled
  stall_timeout: 0s               # 0 means 2 × collection_interval + 1h; otherwise must exceed collection_interval
  unauthorized_cycles: 3          # consecutive cycles answered only with 401 before /healthz fails
telemetry:                        # self-telemetry, see "Self-telemetry"
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
      - org: my-org
        include_repos: []
        exclude_repos: []
        include_archived: false
    repos:
      - other-owner/some-repo
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
```

Quote logins that contain brackets in flow lists: `authors: ["renovate[bot]"]`. Unquoted, YAML reads `[bot]` as a nested list.

### Renovate preset

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

### Environment variables

| Env                  | Default         | Description                                                  |
| -------------------- | --------------- | ------------------------------------------------------------ |
| `CONFIG_FILE`        | `config.yaml`   | Path to the config file                                      |
| `GITHUB_TOKEN`       |                 | PAT for targets with an empty or omitted `auth` block        |
| `RENOVATE_BOT_LOGIN` | `renovate[bot]` | Login of the Renovate bot that owns the Dependency Dashboard |

A target's credential is resolved in this order:

1. The target's own `auth.token`, `auth.token_env` or `auth.app`. `GITHUB_TOKEN` is then ignored for that target.
1. `GITHUB_TOKEN`, when the target's `auth` block is empty or omitted. Every such target shares it. If it is unset or empty, startup fails with an error naming the target.

A partially filled `auth` block (for example `app` without `app_id`) is a startup error; it does not fall back to `GITHUB_TOKEN`.

`base_url` drives every GitHub call for the target: REST, GraphQL, the GitHub App token exchange and the startup credential check. GraphQL goes to `https://api.github.com/graphql` by default and to `https://<host>/api/graphql` when `base_url` ends in `/api/v3`. Targets with different `base_url` values can run in one deployment.

Scalar config keys can be overridden with `GITHUB_ACTIVITY_EXPORTER_` env vars, upper-cased with `.` replaced by `_` (for example `GITHUB_ACTIVITY_EXPORTER_EXPORT_OTLP_ENDPOINT`). Per-target settings, including `auth`, cannot be overridden this way.

## Health endpoints

The exporter always serves `/healthz` and `/readyz` on `health.listen_address` (default `:10062`), whether or not the Prometheus bridge is enabled. The server starts before the startup credential checks. Both endpoints answer `GET` and `HEAD` with `200 ok`, or `503` with one line per failing target and the reason.

`/readyz` returns 200 once every target has passed its startup credential check and completed at least one collection cycle in which repo discovery succeeded. Failures of single repos do not block it. Once ready, the exporter stays ready.

`/healthz` returns 503 when, for any target:

- its collection loop exited before shutdown;
- no collection cycle completed within `health.stall_timeout` of the previous one, or of the loop start. The default, `2 × collection_interval + 1h` (1h10m at 5m), leaves room for a full GitHub rate-limit pause;
- its last `health.unauthorized_cycles` cycles (default 3) were all unauthorized. A cycle is unauthorized when GitHub answered at least one request, including the GitHub App token exchange, with 401 and none with any other status. A cycle with any non-401 response clears the count; a cycle with no GitHub response leaves it as is.

A 401 never affects `/readyz`. A credential revoked while running fails `/healthz`; after a restart the startup credential check fails and the exporter exits.

The Prometheus bridge serves only `/metrics`.

## Self-telemetry

Self-telemetry is telemetry about the exporter itself: metrics, traces, logs and profiles. It is separate from the GitHub metrics the exporter exports, and it is off by default.

```yaml
telemetry:
  enabled: false        # global toggle; false turns every signal below off
  metrics:
    enabled: true
  traces:
    enabled: true
  logs:
    enabled: true
  profiles:
    enabled: true
```

A signal is active only when `telemetry.enabled` and its own `enabled` are both true, `OTEL_SDK_DISABLED` is not `true`, and, for traces, metrics and logs, `OTEL_<SIGNAL>_EXPORTER` is not `none`. Environment variables can turn a signal off, never on. Every key can be overridden with `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_ENABLED` and `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_<SIGNAL>_ENABLED`; a value that is not a boolean is a startup error. The startup log lists the active signals under `self_telemetry`.

This release adds the configuration, the providers and the resource. The signals themselves come in [#74](https://github.com/gjed/github-activity-exporter/issues/74) (runtime and HTTP client metrics), [#75](https://github.com/gjed/github-activity-exporter/issues/75) (traces), [#76](https://github.com/gjed/github-activity-exporter/issues/76) (OTLP logs) and [#77](https://github.com/gjed/github-activity-exporter/issues/77) (profiles).

Self-telemetry runs on its own OTel SDK providers. No `telemetry` key and no `OTEL_*` variable other than `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` changes what the GitHub metrics export, where, how or when: their OTLP push is configured by `export.otlp` only. The `github_exporter.*` metrics (`scrape.duration`, `api.errors`, `otlp.errors`, `api.rate_limit.*`) belong to the GitHub metrics and are always on; the dashboard needs them. When the Prometheus bridge is enabled, self-telemetry metrics are also served on `/metrics`.

Traces, metrics and logs go over OTLP. With no OTLP endpoint in the environment, each signal uses `export.otlp`: same endpoint, protocol, headers and TLS. For `protocol: http` the endpoint is a base URL, and a trailing `/v1/metrics` is dropped, so `https://gw.example.com/otlp` and `https://gw.example.com/otlp/v1/metrics` both send traces to `https://gw.example.com/otlp/v1/traces`. Once `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_<SIGNAL>_ENDPOINT` is set, that signal is configured from the `OTEL_EXPORTER_OTLP_*` variables only, and `export.otlp` headers are never sent to it.

The standard [OTel SDK variables](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/) apply, signal-specific before generic:

| Env                                                                                | Effect                                                                                                           |
| ---------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `OTEL_SDK_DISABLED`                                                                | `true` turns all self-telemetry off. The GitHub metrics keep exporting                                           |
| `OTEL_TRACES_EXPORTER`, `OTEL_METRICS_EXPORTER`, `OTEL_LOGS_EXPORTER`              | `none` turns the signal off; `otlp` is the default. Other values log a warning and mean `otlp`                   |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_ENDPOINT` | Destination; replaces `export.otlp` for the signal                                                               |
| `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_PROTOCOL` | `grpc` or `http/protobuf` (default) when an endpoint variable is set. Ignored when the signal uses `export.otlp` |
| `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_HEADERS`   | Request headers; replace `export.otlp.headers`                                                                   |
| `OTEL_EXPORTER_OTLP_TIMEOUT`, `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_TIMEOUT`   | Export timeout in milliseconds                                                                                   |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG`                                   | Trace sampler; default `parentbased_always_on`                                                                   |
| `OTEL_METRIC_EXPORT_INTERVAL`                                                      | Self-telemetry metric push interval in milliseconds; default 60000                                               |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`                                    | Resource attributes, for the GitHub metrics too                                                                  |

Every provider, the GitHub metrics included, shares one resource: `service.name` (default `github-activity-exporter`), `service.version` (the build version), `service.instance.id` (a random UUID per process) and the host, OS, process and container attributes. `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` override them. The instance ID changes on every restart; set a stable one, such as the pod name, with `OTEL_RESOURCE_ATTRIBUTES=service.instance.id=<id>` if your backend derives `instance` from it. Every meter, tracer and logger uses the instrumentation scope `github.com/gjed/github-activity-exporter` with the build version.

On SIGTERM or SIGINT the exporter flushes and shuts down every self-telemetry provider after the GitHub metrics, within the same 30 s deadline.

## Metrics

All metric names and attributes are defined in the OTel registry at [`registry/`](registry/).

| Metric                            | Instrument    | Description                                          |
| --------------------------------- | ------------- | ---------------------------------------------------- |
| `github.pr.count`                 | UpDownCounter | Open PR count by repo, state (open, draft) and label |
| `github.pr.age`                   | Gauge         | Age in seconds of the oldest open PR per repo        |
| `github.pr.created`               | Counter       | PRs created                                          |
| `github.pr.merged`                | Counter       | PRs merged                                           |
| `github.pr.closed`                | Counter       | PRs closed without merge                             |
| `github.pr.close.duration`        | Histogram     | Time from open to close/merge                        |
| `github.pr.automerged`            | Counter       | PRs merged with no human approval                    |
| `github.pr.review_status`         | Gauge         | Open PR count by review decision state               |
| `github.issue.count`              | UpDownCounter | Issue count by repo, state, and label                |
| `github.issue.age`                | Gauge         | Age in seconds of the oldest open issue per repo     |
| `renovate.dashboard.queue`        | Gauge         | Renovate dashboard PRs by queue state                |
| `github_exporter.scrape.duration` | Histogram     | Collection cycle duration in seconds                 |
| `github_exporter.api.errors`      | Counter       | GitHub API errors by target and endpoint             |
| `github_exporter.otlp.errors`     | Counter       | OTLP push failures                                   |

### PR events

Each cycle the exporter reads every open PR of a repo, with no limit, for `github.pr.count`, `github.pr.age` and `github.pr.review_status`. It then reads PRs by last update, newest first, back to the start of the previous cycle's window, and counts the events of the selected PRs that it has not counted yet:

- `github.pr.created`, `github.pr.merged`, `github.pr.closed` (closed without merge; merged PRs count only in `github.pr.merged`), `github.pr.close.duration` and `github.pr.automerged`.
- The window runs from the start of the repo's previous successful cycle, capped at 30 days back, to the start of the current cycle. Each event is counted once; a PR updated while the scan pages, and so missed, is counted by the next cycle.
- A repo's first cycle records no events. A failed cycle records none and the next successful cycle covers the gap.
- Counters reset when the exporter restarts. Events that happen while it is down are not counted.

Rates, globally and per repo:

```promql
sum(rate({"github.pr.merged"}[1h]))
sum by ("github.org", "github.repo") (increase({"github.pr.created"}[1d]))
```

Closed and merged totals are not exported as gauges. Use `increase({"github.pr.merged"}[30d])` instead of `{"github.pr.count", "github.pr.state"="merged"}`.

### Metric naming

The registry name is the stored name ([#14](https://github.com/gjed/github-activity-exporter/issues/14)). OTLP and the Prometheus bridge expose `github.pr.count` as `github.pr.count`: no `_total` or unit suffix (`_seconds`), no underscore escaping. Histograms add only the classic sample series `<name>_bucket`, `<name>_sum` and `<name>_count`, for example `github_exporter.scrape.duration_bucket`. CI checks both outputs against the registry.

Your backend must keep the names as sent (OTLP translation strategy `NoTranslation`):

- Prometheus 3.x: UTF-8 metric names (`metric_name_validation_scheme: utf8`, the 3.x default). For OTLP ingestion, run with `--web.enable-otlp-receiver` and set `otlp.translation_strategy: NoTranslation`.
- Mimir: `-validation.name-validation-scheme=utf8`, `-distributor.otel-translation-strategy=NoTranslation` and `-distributor.otel-metric-suffixes-enabled=false` (per-tenant limits `name_validation_scheme`, `otel_translation_strategy`, `otel_metric_suffixes_enabled`).
- Grafana Cloud Metrics: the same Mimir per-tenant limits on your stack.

With any other setting the backend stores escaped or suffixed names (for example `github_pr_count`), which do not match the registry.

`/metrics` does not serve the OpenMetrics text format, because OpenMetrics requires counter names to end in `_total`. Scrapers that ask for OpenMetrics first, as Prometheus 3.x and Alloy do by default, get the Prometheus text format, which keeps the `counter` type.

## Development

### Prerequisites

- Go 1.26+
- [golangci-lint](https://golangci-lint.run/usage/install/)
- [OTel Weaver](https://github.com/open-telemetry/weaver/releases) (for schema validation)

### Build

```bash
go build ./...
```

### Test

```bash
go test -race ./...
```

### Lint

```bash
golangci-lint run
```

### Validate OTel registry

```bash
weaver registry check -r registry/
```

### Generate code from registry

```bash
make generate
```

## CI

| Job                 | Trigger                   | Description                                               |
| ------------------- | ------------------------- | --------------------------------------------------------- |
| `lint`              | every PR + push to `main` | golangci-lint                                             |
| `test`              | every PR + push to `main` | `go test -race`, JUnit XML upload, coverage PR comment    |
| `build`             | every PR + push to `main` | `go build ./...` for `linux/amd64`                        |
| `docker-build`      | every PR + push to `main` | Multi-arch image build (`linux/amd64`, `linux/arm64`)     |
| `check-generated`   | every PR + push to `main` | Fails if `make generate` produces uncommitted changes     |
| `registry-validate` | every PR + push to `main` | Weaver schema check + emit smoke-test (separate workflow) |
| `integration-test`  | PRs targeting `main`      | Starts exporter + mock GitHub API, runs Weaver live-check |
| `release`           | push to `main`            | semantic-release → version tag + GitHub Release           |
| `image`             | release published         | Multi-arch image → GHCR (`X.Y.Z`, `X.Y`, `X`, `latest`)   |
| `chart check`       | PRs touching the chart    | `helm lint`, `helm template`, `kubeconform -strict`       |
| `chart publish`     | release published         | Helm chart `X.Y.Z` → `oci://ghcr.io/gjed/charts`          |

## Release

Releases are fully automated. Merge to `main` and semantic-release determines the next version from [conventional commits](https://www.conventionalcommits.org/):

| Commit prefix                 | Version bump |
| ----------------------------- | ------------ |
| `feat:`                       | minor        |
| `fix:`, `perf:`               | patch        |
| `feat!:` / `BREAKING CHANGE:` | major        |
| `chore:`, `docs:`, `test:`    | no release   |

## License

[Apache 2.0](LICENSE)
