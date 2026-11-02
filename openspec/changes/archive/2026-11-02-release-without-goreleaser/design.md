## Context

See proposal.md - Why. Today `release.yml` runs semantic-release, which pushes the tag and then calls GoReleaser through `@semantic-release/exec`; GoReleaser builds binaries, creates the GitHub Release and pushes per-arch images plus manifests. The `Dockerfile` only copies a prebuilt binary.

## Goals / Non-Goals

**Goals:**

- Tag and GitHub Release are created by one tool in one step.
- An image failure is recovered by re-running `image.yml` alone.
- `docker run <image> --version` prints the release version.

**Non-Goals:**

- Injecting `commit`/`date` into the binary (`--version` keeps `none`/`unknown` for those; OCI labels carry revision and created).
- Build caching, SBOM/provenance tuning, signing.
- Touching existing releases or other CI jobs.

## Decisions

- **`@semantic-release/github` over `exec` + GoReleaser.** It creates the release with the generated notes, so `write-release-notes.js` goes too. Added as an explicit devDependency (as the other plugins are) rather than relying on semantic-release's transitive copy under pnpm.
- **Separate `image.yml` on `release: published`.** Decouples the image from the tag/release step. The event fires only because the release is created with the `gjed-release` app token; `GITHUB_TOKEN`-created events don't trigger workflows. `release.yml` drops `packages: write` and `id-token: write` since it no longer pushes images.
- **`docker/metadata-action` tags:** `type=semver` with `{{version}}`, `{{major}}.{{minor}}`, `{{major}}`; `latest` comes from `flavor: latest=auto`. On a `release` event `github.ref` is `refs/tags/vX.Y.Z`, so semver parsing works. Its `version` output (`X.Y.Z`, no `v`, same as GoReleaser's `{{.Version}}`) is passed as the `VERSION` build-arg.
- **Cross-compile, no QEMU.** Build stage `FROM --platform=$BUILDPLATFORM golang:1.25`, `CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w -X main.version=$VERSION"`. The final stage has no `RUN`, so the arm64 stage never executes anything. `VERSION` defaults to `dev`, matching the in-code default.
- **CI check:** `docker/build-push-action` with `push: false` for both platforms after `docker/setup-buildx-action`. Replaces `goreleaser-check` one-for-one.
- **Action pinning:** every action in the new/changed jobs is pinned by full commit SHA with a trailing `# vX.Y.Z` comment, as `create-github-app-token` is today, so Renovate's `github-actions` manager keeps them updated.

## Risks / Trade-offs

- \[`{{major}}` emits tag `0` while on v0.x\] → Kept as requested (`X`); metadata-action documents an `enable=` guard if that is unwanted.
- [Release published but image fails] → Release exists; re-run `image.yml`.
- \[App token missing `contents: write`\] → Unchanged from today; the token already creates releases.
- \[Merging a `ci:` commit triggers no release\] → The first release on the new pipeline happens with the next `feat`/`fix`.

## Migration Plan

Merge to `main`. The next releasable commit exercises `release.yml` then `image.yml`. Rollback: revert the merge commit.
