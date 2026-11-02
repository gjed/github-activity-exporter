## Why

Only the OCI image is needed. GoReleaser builds archives and darwin binaries nobody uses, and semantic-release pushes the tag before GoReleaser creates the GitHub Release, so a GoReleaser failure leaves a tag without a release (v0.0.1). Its `dockers`/`docker_manifests` path is also legacy, so keeping it would mean a migration anyway.

## What Changes

- `release.yml`: semantic-release with `@semantic-release/github` pushes the tag and creates the GitHub Release with the conventional-commits notes. No Go, Docker or GoReleaser steps. The `gjed-release` app token stays so the release event triggers workflows.
- New `image.yml` on `release: published`: builds `linux/amd64` + `linux/arm64` and pushes one multi-arch image to GHCR tagged `X.Y.Z`, `X.Y`, `X` and `latest`, with OCI labels.
- `Dockerfile`: multi-stage, cross-compiles on the build platform, injects the release version so `--version` reports it. Final stage stays `distroless/static:nonroot` with `CONFIG_FILE` set.
- CI: the `goreleaser-check` job is replaced by a multi-arch `docker build` check.
- Remove `.goreleaser.yaml`, `scripts/write-release-notes.js` and `@semantic-release/exec`.
- **BREAKING** for binary downloaders: releases no longer carry archives or checksums, and darwin builds are gone. `go install` and the image remain. Existing v0.0.2 assets and the manual v0.0.1 release are left as they are.

## Capabilities

### New Capabilities

### Modified Capabilities

- `ci-release`: semantic-release creates the GitHub Release; the image is built on `release: published`; binary archives, checksums and darwin builds are dropped.

## Impact

- `.github/workflows/release.yml`, `.github/workflows/ci.yml`, new `.github/workflows/image.yml`
- `Dockerfile`, `.releaserc.js`, `package.json`, `pnpm-lock.yaml`
- Removed: `.goreleaser.yaml`, `scripts/write-release-notes.js`
- `README.md` (binary install, prerequisites, CI table), `.gitignore` (GoReleaser entries), `cmd/renovate-exporter/main.go` (ldflags comment)
- `openspec/specs/ci-release/spec.md` Purpose
