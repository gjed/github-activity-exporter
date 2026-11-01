## Why

Without CI, the codebase will drift: schema violations go undetected, tests go unrun, and releases require manual steps. This change establishes GitHub Actions workflows that enforce quality gates on every PR and automate releases. The Weaver schema check is a first-class CI job — schema drift is a build failure, not a code review concern.

## What Changes

- `.github/workflows/ci.yml`: PR and push workflow — lint, test, build, Weaver schema check, Docker build
- `.github/workflows/release.yml`: tag-triggered workflow — multi-arch binary build via GoReleaser, Docker image push to GHCR
- `.github/workflows/generate-check.yml`: verifies that generated files (`internal/semconv/`, `dashboards/`) are up to date (no stale codegen)
- `renovate.json`: self-hosted Renovate config for dependency updates on this repo

## Capabilities

### New Capabilities

- `ci-checks`: PR gate — lint (`golangci-lint`), unit tests with coverage, build verification, Weaver registry check, stale-codegen detection
- `ci-release`: Tag-triggered — GoReleaser multi-arch binaries (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64), Docker image to GHCR, GitHub Release with checksums
- `ci-integration`: Integration test job — starts exporter with mock config, runs Weaver live-check

### Modified Capabilities

## Impact

- New directory: `.github/workflows/`
- New files: `.goreleaser.yaml`, `renovate.json`
- Parallel with: `observability-contract`, `github-client` (CI can be set up before all code exists)
