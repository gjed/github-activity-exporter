## 1. CI Check Workflow

- [x] 1.1 Create `.github/workflows/ci.yml` with triggers: `pull_request` (all branches) and `push` to `main`
- [x] 1.2 Add `lint` job: `golangci/golangci-lint-action@v6`, Go cache, `ubuntu-latest`
- [x] 1.3 Create `.golangci.yml` with linters: `errcheck`, `govet`, `staticcheck`, `gofmt`, `goimports`, `revive`
- [x] 1.4 Add `test` job: `go test -race -coverprofile=coverage.out -covermode=atomic ./...`; convert to JUnit XML with `gotestsum`; upload XML as artifact
- [x] 1.5 Add coverage annotation step: post coverage % as PR comment using `actions/github-script` (non-blocking)
- [x] 1.6 Add `build` job: `GOOS=linux GOARCH=amd64 go build ./...`
- [x] 1.7 Add `check-generated` job: run `make generate`, then `git diff --exit-code` — fails if semconv or dashboards are stale
- [x] 1.8 Add job dependency order: `lint` and `check-generated` run in parallel; `test` runs after `lint`; `build` runs after `test`

## 2. Telemetry Registry Validation Workflow

- [x] 2.1 Create `.github/workflows/registry-validate.yml` with triggers: `pull_request` (all branches) and `push` to `main`
- [x] 2.2 Add `registry-validate` job on `ubuntu-latest`
- [x] 2.3 Add step: install Weaver binary (pin version via download from releases page; use composite action `.github/actions/setup-weaver/action.yml`)
- [x] 2.4 Add step: `weaver registry check -r registry/` — fails job on any schema error
- [x] 2.5 Add step: `weaver registry resolve -r registry/` — dump resolved schema as artifact for inspection
- [x] 2.6 Add step: `weaver registry emit -r registry/` with `--inactivity-timeout 10` — smoke-tests that example telemetry can be generated from the schema
- [x] 2.7 Write composite action `.github/actions/setup-weaver/action.yml` with `weaver-version` input; downloads correct binary for runner OS/arch; adds to PATH
- [ ] 2.8 Add `registry-validate` as a required status check in branch protection rules for `main`

## 3. Integration Test Job

- [x] 3.1 Add `integration-test` job to `ci.yml`, conditioned on `github.base_ref == 'main'`
- [x] 3.2 Reuse `.github/actions/setup-weaver/action.yml` for Weaver installation
- [ ] 3.3 Add step: build exporter binary
- [ ] 3.4 Add step: start mock GitHub API server (provided by `tests/integration/mockserver/`)
- [ ] 3.5 Add step: start exporter with test config pointing at mock server; wait for `/healthz` to respond
- [x] 3.6 Add step: `weaver registry live-check -r registry/ --inactivity-timeout 30` — fails if emitted metrics violate schema
- [ ] 3.7 Add step: stop exporter and mock server (use `jobs.<id>.steps` with `if: always()` for cleanup)
- [x] 3.8 Add `integration-test` as an advisory (non-blocking) status check in MVP; upgrade to required after first stable run

## 4. Release Workflow (semantic-release + GoReleaser)

- [x] 4.1 Create `package.json` at repo root with semantic-release plugins: `@semantic-release/commit-analyzer`, `@semantic-release/release-notes-generator`, `@semantic-release/changelog`, `@semantic-release/exec`, `@semantic-release/git`, `conventional-changelog-conventionalcommits`
- [ ] 4.2 Create `.releaserc.js` (or `.releaserc.json`): branches `[main]`; `commit-analyzer` preset `conventionalcommits`; `exec` plugin calls `goreleaser release --clean --release-notes /tmp/release-notes.md` with `GORELEASER_CURRENT_TAG` and `GORELEASER_PREVIOUS_TAG` env vars
- [ ] 4.3 Create `.goreleaser.yaml`: `builds` for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`; `archives` with tarballs; `checksum`; `changelog.skip: true` (semantic-release owns changelog); `dockers` + `docker_manifests` for GHCR multi-arch image
- [x] 4.4 Create `.github/workflows/release.yml` triggered on `push` to `main`
- [x] 4.5 Add `release` job: checkout with `fetch-depth: 0` (semantic-release needs full git history); setup Node.js LTS; `npm ci`; run `npx semantic-release` with `GITHUB_TOKEN` and `CR_PAT` (for GHCR push if needed)
- [x] 4.6 Add `permissions` to release job: `contents: write`, `packages: write`, `id-token: write`
- [x] 4.7 Add Docker buildx setup step in release job for multi-arch image build
- [ ] 4.8 Test the full flow on a throwaway branch: merge a `feat:` commit to a test branch configured in `.releaserc.js`, verify release is created
- [x] 4.9 Add `package-lock.json` to `.gitignore` exclusion list (commit it — semantic-release needs reproducible installs)

## 5. Repository Setup

- [ ] 5.1 Add `renovate.json` extending the org shared config for self-managed dependency updates on this repo
- [x] 5.2 Add custom Renovate manager for Weaver version in `.github/actions/setup-weaver/action.yml`
- [x] 5.3 Add custom Renovate manager for Node.js semantic-release plugin versions in `package.json`
- [ ] 5.4 Configure branch protection on `main`: require `lint`, `test`, `build`, `check-generated`, `registry-validate` to pass; require linear history; no direct push
- [x] 5.5 Add `CODEOWNERS` file
- [x] 5.6 Add `LICENSE` (Apache 2.0)
- [x] 5.7 Add `.github/dependabot.yml` as fallback for GitHub Actions version updates (or rely on Renovate — pick one, document the choice)
