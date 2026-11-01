## Context

GitHub Actions is the CI platform. Four workflows cover the full lifecycle: PR gate (`ci.yml`), telemetry registry validation (`registry-validate.yml`), integration tests (job in `ci.yml`, runs on PRs to `main`), and release automation (`release.yml`). Weaver is installed as a binary in CI (not via `go install` — it's a Rust binary) via a shared composite action. Release automation uses `semantic-release` to determine versions from conventional commits, then hands off to GoReleaser for artifact production.

## Goals / Non-Goals

**Goals:**

- Every PR runs: lint, unit tests (with `-race`), build, stale-codegen check
- Registry validation is a dedicated separate status check (`registry-validate.yml`): `weaver registry check`, `weaver registry emit` smoke test
- Release is fully automated on merge to `main` via semantic-release: no manual tagging; version determined from conventional commits
- GoReleaser produces multi-arch binaries, Docker image (GHCR), checksums; invoked by semantic-release
- Integration test job runs Weaver live-check against a locally started exporter (PRs to `main` only)
- Self-hosting: this repo uses Renovate for its own dependencies

**Non-Goals:**

- Deployment (no CD — just artifact publication)
- Performance benchmarks in CI
- Windows builds (Linux + macOS sufficient for the target audience)

## Decisions

### Decision: `golangci-lint` for linting

Rationale: Industry standard for Go; covers `go vet`, `staticcheck`, `errcheck`, and many more. Configured via `.golangci.yml`. Run as a GitHub Action using `golangci/golangci-lint-action`.

### Decision: semantic-release drives versioning, GoReleaser drives artifacts

Rationale: semantic-release analyzes conventional commits and determines the next version automatically — no manual tagging, no version drift. It calls GoReleaser via `@semantic-release/exec` with `GORELEASER_CURRENT_TAG` set, GoReleaser handles cross-compilation, archiving, checksum, Docker, and GitHub Release creation. The two tools are complementary: semantic-release owns the version decision and changelog; GoReleaser owns the build pipeline.
Tradeoff: requires Node.js in the release runner and a `package.json` at repo root. This is a CI-only dependency — no impact on the Go application.
Alternative considered: GoReleaser alone with manual tags — rejected because it requires a human to decide and push the tag, which is error-prone and inconsistent.

### Decision: Telemetry registry validation is a dedicated workflow, not a job in `ci.yml`

Rationale: Makes schema validation visible as an independent required status check in GitHub. PRs that only touch `registry/` still get a clear, named check result. Separating it also allows the registry check to be reused in other contexts (e.g., pre-commit hook) without coupling it to the full CI pipeline.

### Decision: `gotestsum` for test output, JUnit XML upload

Rationale: `gotestsum` wraps `go test` with better output formatting and produces JUnit XML natively. The XML is uploaded as a CI artifact, making test failures inspectable without scrolling raw logs. Minimal overhead.

### Decision: GoReleaser for release automation

Rationale: Handles cross-compilation, archive creation, checksum generation, GitHub Release creation, and Docker image push in one tool. Config via `.goreleaser.yaml`. Invoked by semantic-release, not directly triggered by a tag push.

### Decision: Docker image to GHCR (ghcr.io/gjed/renovate-exporter)

Rationale: GHCR is free for public repos, no DockerHub rate limits, authenticated with `GITHUB_TOKEN` (no separate secret needed). Multi-arch manifest via `docker buildx`.

### Decision: Stale codegen check via `make generate && git diff --exit-code`

Rationale: Simplest possible check — regenerate everything and fail if there are uncommitted changes. Prevents "someone edited `internal/semconv/` by hand" bugs.

### Decision: Weaver installed via shared composite action, not `go install`

Rationale: Weaver is a Rust binary, not a Go package. A composite action at `.github/actions/setup-weaver/` downloads and pins the version, and is reused by both `registry-validate.yml` and the integration test job. Renovate tracks the pinned version via a custom manager.

## Risks / Trade-offs

- **Node.js in release runner** → CI-only; `package.json` and `package-lock.json` at root are pure tooling; add `node_modules/` to `.gitignore`
- **semantic-release commits back to main** → The `chore(release): <version>` commit and `CHANGELOG.md` update require write access; handled by `GITHUB_TOKEN` with `contents: write` permission; branch protection must allow the Actions bot to push
- **Weaver version pinning** → Must be updated when Weaver adds schema features; tracked by Renovate custom manager
- **Integration test job is slow** → Run only on PRs targeting `main`, not every feature branch push
- **GoReleaser requires `GITHUB_TOKEN` write permission** → Granted automatically by `permissions: contents: write, packages: write` in release workflow
