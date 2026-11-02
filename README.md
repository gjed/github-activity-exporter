# renovate-exporter

[![CI](https://github.com/gjed/renovate-exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/gjed/renovate-exporter/actions/workflows/ci.yml)
[![Registry Validate](https://github.com/gjed/renovate-exporter/actions/workflows/registry-validate.yml/badge.svg)](https://github.com/gjed/renovate-exporter/actions/workflows/registry-validate.yml)
[![Release](https://github.com/gjed/renovate-exporter/actions/workflows/release.yml/badge.svg)](https://github.com/gjed/renovate-exporter/actions/workflows/release.yml)
[![Latest Release](https://img.shields.io/github/v/release/gjed/renovate-exporter)](https://github.com/gjed/renovate-exporter/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/gjed/renovate-exporter)](go.mod)
[![License](https://img.shields.io/github/license/gjed/renovate-exporter)](LICENSE)

GitHub metrics exporter for [Renovate](https://docs.renovatebot.com/) — collects PR and issue metrics from the GitHub API and pushes them via OTLP to any OpenTelemetry-compatible backend (Grafana Cloud, Prometheus with OTLP receiver, Datadog, etc.).

## Features

- Pushes PR and issue metrics via **OTLP** (HTTP or gRPC) as primary output
- Optional Prometheus `/metrics` bridge for scrape-based setups (disabled by default)
- Metrics conform to the [OTel semantic convention registry](registry/) and are validated by [Weaver](https://github.com/open-telemetry/weaver) in CI
- Multi-arch Docker image published to [GHCR](https://ghcr.io/gjed/renovate-exporter)
- Fully automated releases via [semantic-release](https://semantic-release.gitbook.io/) — no manual tagging

## Quick Start

Create `config.yaml`:

```yaml
export:
  otlp:
    endpoint: http://my-otel-collector:4318
targets:
  - name: my-org
    auth:
      token_env: GITHUB_TOKEN
    orgs:
      - org: my-org
```

Run the image with the config mounted at `/etc/renovate-exporter/config.yaml`:

```bash
docker run --rm \
  -e GITHUB_TOKEN=ghp_... \
  -v "$PWD/config.yaml:/etc/renovate-exporter/config.yaml:ro" \
  ghcr.io/gjed/renovate-exporter:latest
```

Metrics are pushed to the OTLP endpoint after each collection cycle (default: every 5 minutes).

To also expose a Prometheus scrape endpoint, set `export.prometheus.enabled: true` in the config and add `-p 9090:9090`.

## Installation

### Docker

```bash
docker pull ghcr.io/gjed/renovate-exporter:latest
```

### Binary

Download the latest release for your platform from the [releases page](https://github.com/gjed/renovate-exporter/releases/latest), then:

```bash
tar -xzf renovate-exporter_*_linux_amd64.tar.gz
chmod +x renovate-exporter
./renovate-exporter --help
```

### From source

```bash
go install github.com/gjed/renovate-exporter/cmd/renovate-exporter@latest
```

## Configuration

The exporter takes no flags other than `--version`. Targets, GitHub auth and export settings come from a YAML file.

### Config file

The path is read from `CONFIG_FILE`. It defaults to `config.yaml` in the working directory, and to `/etc/renovate-exporter/config.yaml` in the Docker image.

```yaml
collection_interval: 5m           # how often to collect and push metrics
export:
  otlp:
    endpoint: https://otlp.example.com:4318  # required; the exporter exits if unset
    protocol: http                # http or grpc
    headers:                      # sent on every OTLP request; names are lower-cased
      Authorization: Bearer <token>
      X-Scope-OrgID: my-tenant
    tls:
      insecure: false             # http: skip cert verification; grpc: no TLS. Not for production
  prometheus:
    enabled: false                # optional /metrics bridge
    listen_address: ":9090"
targets:
  - name: my-org                  # unique per target
    auth:                         # exactly one of token, token_env, or app
      token_env: GITHUB_TOKEN     # env var holding a PAT with repo read access
      # app:
      #   app_id: 12345
      #   installation_id: 67890
      #   private_key_path: /etc/renovate-exporter/key.pem
      #   private_key_env: GITHUB_APP_KEY   # base64-encoded PEM, instead of the path
    orgs:                         # at least one org or repo is required
      - org: my-org
        include_repos: []
        exclude_repos: []
        include_archived: false
    repos:
      - other-owner/some-repo
    filters:
      prs:
        include_labels: []
        exclude_labels: []
        states: []                # open, closed, draft, merged
      issues:
        exclude_title_patterns: [] # regular expressions
        states: []                # open, closed
```

### Environment variables

| Env                  | Default         | Description                                                  |
| -------------------- | --------------- | ------------------------------------------------------------ |
| `CONFIG_FILE`        | `config.yaml`   | Path to the config file                                      |
| `RENOVATE_BOT_LOGIN` | `renovate[bot]` | Login of the Renovate bot that owns the Dependency Dashboard |

## Metrics

All metric names and attributes are defined in the OTel registry at [`registry/`](registry/).
When the Prometheus bridge is enabled, OTel metric names are translated to Prometheus naming conventions automatically.

| Metric                            | Instrument    | Description                                              |
| --------------------------------- | ------------- | -------------------------------------------------------- |
| `github.pr.count`                 | UpDownCounter | PR count by repo, state, and label                       |
| `github.pr.age`                   | Gauge         | Age in seconds of the oldest open PR per repo            |
| `github.pr.close.duration`        | Histogram     | Time from open to close/merge for PRs in lookback window |
| `github.pr.automerged`            | Counter       | PRs merged with no human approval                        |
| `github.pr.review_status`         | Gauge         | Open PR count by review decision state                   |
| `github.issue.count`              | UpDownCounter | Issue count by repo, state, and label                    |
| `github.issue.age`                | Gauge         | Age in seconds of the oldest open issue per repo         |
| `renovate.dashboard.queue`        | Gauge         | Renovate dashboard PRs by queue state                    |
| `github_exporter.scrape.duration` | Histogram     | Collection cycle duration in seconds                     |
| `github_exporter.api.errors`      | Counter       | GitHub API errors by target and endpoint                 |
| `github_exporter.otlp.errors`     | Counter       | OTLP push failures                                       |

## Development

### Prerequisites

- Go 1.22+
- [golangci-lint](https://golangci-lint.run/usage/install/)
- [OTel Weaver](https://github.com/open-telemetry/weaver/releases) (for schema validation)
- [GoReleaser](https://goreleaser.com/install/) (for release builds)

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
| `check-generated`   | every PR + push to `main` | Fails if `make generate` produces uncommitted changes     |
| `registry-validate` | every PR + push to `main` | Weaver schema check + emit smoke-test (separate workflow) |
| `integration-test`  | PRs targeting `main`      | Starts exporter + mock GitHub API, runs Weaver live-check |
| `release`           | push to `main`            | semantic-release → GoReleaser → GHCR                      |

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
