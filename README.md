# github-activity-exporter

[![CI](https://github.com/gjed/github-activity-exporter/actions/workflows/ci.yml/badge.svg)](https://github.com/gjed/github-activity-exporter/actions/workflows/ci.yml)
[![Registry Validate](https://github.com/gjed/github-activity-exporter/actions/workflows/registry-validate.yml/badge.svg)](https://github.com/gjed/github-activity-exporter/actions/workflows/registry-validate.yml)
[![Release](https://github.com/gjed/github-activity-exporter/actions/workflows/release.yml/badge.svg)](https://github.com/gjed/github-activity-exporter/actions/workflows/release.yml)
[![Latest Release](https://img.shields.io/github/v/release/gjed/github-activity-exporter)](https://github.com/gjed/github-activity-exporter/releases/latest)
[![Go Version](https://img.shields.io/github/go-mod/go-version/gjed/github-activity-exporter)](go.mod)
[![License](https://img.shields.io/github/license/gjed/github-activity-exporter)](LICENSE)

GitHub pull request and issue activity exporter — collects PR and issue metrics from the GitHub API, optionally including [Renovate](https://docs.renovatebot.com/) Dependency Dashboard state, and pushes them via OTLP to any OpenTelemetry-compatible backend (Grafana Cloud, Prometheus with OTLP receiver, Datadog, etc.).

## Features

- Pushes PR and issue [metrics](docs/metrics.md) via **OTLP** (HTTP or gRPC) as primary output
- Optional Prometheus `/metrics` bridge for scrape-based setups (disabled by default)
- Always-on [`/healthz` and `/readyz`](docs/health.md) endpoints for liveness and readiness probes
- Metrics conform to the [OTel semantic convention registry](registry/) and are validated by [Weaver](https://github.com/open-telemetry/weaver) in CI
- Two generated [Grafana dashboards](docs/dashboards.md)
- Optional [self-telemetry](docs/self-telemetry.md): metrics, traces, logs and profiles of the exporter itself
- Multi-arch Docker image published to [GHCR](https://ghcr.io/gjed/github-activity-exporter) and a [Helm chart](charts/github-activity-exporter/README.md)

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

With no `auth` block the target uses the `GITHUB_TOKEN` env var as a PAT. The token needs read access to pull requests and issues; see [GitHub permissions](docs/github-permissions.md).

Run the image with the config mounted at `/etc/github-activity-exporter/config.yaml`:

```bash
docker run --rm \
  -e GITHUB_TOKEN=ghp_... \
  -v "$PWD/config.yaml:/etc/github-activity-exporter/config.yaml:ro" \
  ghcr.io/gjed/github-activity-exporter:latest
```

Metrics are pushed to the OTLP endpoint after each collection cycle (default: every 5 minutes).

To also expose a Prometheus scrape endpoint, set `export.prometheus.enabled: true` in the config and add `-p 9090:9090`.

Every config key is in [Configuration](docs/configuration.md). For a large org, size the collection with [Collection and rate limits](docs/collection.md).

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

## Backend requirements

Metric names are exported as declared in the registry, for example `github.pr.count`, with no `_total` or unit suffix and no underscore escaping (see [Metric naming](docs/metrics.md#metric-naming)). Your backend must keep the names as sent (OTLP translation strategy `NoTranslation`):

- Prometheus 3.x: UTF-8 metric names (`metric_name_validation_scheme: utf8`, the 3.x default). For OTLP ingestion, run with `--web.enable-otlp-receiver` and set `otlp.translation_strategy: NoTranslation`.
- Mimir: `-validation.name-validation-scheme=utf8`, `-distributor.otel-translation-strategy=NoTranslation` and `-distributor.otel-metric-suffixes-enabled=false` (per-tenant limits `name_validation_scheme`, `otel_translation_strategy`, `otel_metric_suffixes_enabled`).
- Grafana Cloud Metrics: the same Mimir per-tenant limits on your stack.

## Documentation

| Page                                                    | Covers                                                         |
| ------------------------------------------------------- | -------------------------------------------------------------- |
| [Configuration](docs/configuration.md)                  | Config file reference, Renovate preset, environment variables  |
| [Collection and rate limits](docs/collection.md)        | Collection schedule, concurrency sizing, GraphQL cost          |
| [GitHub permissions](docs/github-permissions.md)        | Permissions per auth mode, startup check, GitHub App manifest  |
| [Health endpoints](docs/health.md)                      | `/healthz` and `/readyz`                                       |
| [Metrics](docs/metrics.md)                              | Exported metrics, PR events, PR size and author, metric naming |
| [Grafana dashboards](docs/dashboards.md)                | The two generated dashboards                                   |
| [Self-telemetry](docs/self-telemetry.md)                | Metrics, traces, logs and profiles about the exporter itself   |
| [Helm chart](charts/github-activity-exporter/README.md) | Chart values, Secrets, probes, examples                        |

## Contributing

Build, test, code generation, CI and releases are in [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[Apache 2.0](LICENSE)
