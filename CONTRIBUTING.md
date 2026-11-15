# Contributing

Usage and configuration are in the [README](README.md) and its [documentation](README.md#documentation) pages. This page covers working on the code.

## Prerequisites

- Go 1.26+
- [golangci-lint](https://golangci-lint.run/usage/install/)
- [OTel Weaver](https://github.com/open-telemetry/weaver/releases) (for schema validation and code generation; `make install-weaver`)
- Docker, for `make test-e2e`

## Build

```bash
go build ./...
```

## Test

```bash
go test -race ./...
```

`make test-e2e` (Docker required) runs the dashboard end-to-end check described in [Grafana dashboards](docs/dashboards.md).

## Lint

```bash
golangci-lint run
```

## Validate OTel registry

Metric, attribute and span names live in the Weaver registry under [`registry/`](registry/); see [Metrics](docs/metrics.md) for the naming contract.

```bash
make check-schema
```

## Generate code from registry

```bash
make generate
```

`internal/semconv/*.go` and `dashboards/*.json` are generated from the registry and the templates under [`templates/`](templates/). Never edit them by hand: change the registry or the templates and run `make generate`. CI fails if the generated files differ from what is committed.

## Changes and specs

Behaviour changes are proposed and specified with [OpenSpec](https://github.com/Fission-AI/OpenSpec) under [`openspec/`](openspec/); see [AGENTS.md](AGENTS.md) for the workflow.

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
| `dashboard-e2e`     | every PR + push to `main` | Runs every dashboard query against Prometheus 3.x         |
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
