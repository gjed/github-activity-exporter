## 1. Image build

- [ ] 1.1 Rewrite `Dockerfile` as multi-stage: `FROM --platform=$BUILDPLATFORM golang:1.25` build stage cross-compiling with `TARGETOS`/`TARGETARCH`, `CGO_ENABLED=0`, `-ldflags "-s -w -X main.version=${VERSION}"` from `ARG VERSION=dev`; final stage `gcr.io/distroless/static:nonroot` with `CONFIG_FILE` set. Verify `docker buildx build --platform linux/amd64,linux/arm64 .` succeeds and `docker run <image> --version` prints the injected `VERSION`
- [ ] 1.2 Update the ldflags comment in `cmd/renovate-exporter/main.go` to point at the Dockerfile; verify `go build ./...`
- [ ] 1.3 Add `.github/workflows/image.yml` on `release: published`: checkout, setup-buildx, GHCR login, `docker/metadata-action` (`{{version}}`, `{{major}}.{{minor}}`, `{{major}}`, `latest`, OCI labels), `docker/build-push-action` for `linux/amd64,linux/arm64` with `VERSION` build-arg; `permissions: contents: read, packages: write`; actions pinned by SHA. Verify with `actionlint`

## 2. Release

- [ ] 2.1 Replace `./scripts/write-release-notes.js` and `@semantic-release/exec` with `@semantic-release/github` in `.releaserc.js`; delete `scripts/write-release-notes.js`
- [ ] 2.2 Swap `@semantic-release/exec` for `@semantic-release/github` in `package.json` and refresh `pnpm-lock.yaml`; verify `pnpm install --frozen-lockfile`
- [ ] 2.3 Strip Go, GoReleaser, Buildx and GHCR steps from `release.yml`, drop `packages: write` and `id-token: write`, update comments, pin remaining actions by SHA. Verify with `actionlint`
- [ ] 2.4 Verify `pnpm exec semantic-release --dry-run --no-ci` loads `@semantic-release/github` without plugin errors

## 3. CI and cleanup

- [ ] 3.1 Replace the `goreleaser-check` job in `ci.yml` with a `docker-build` job (setup-buildx + build-push-action, `push: false`, both platforms), pinned by SHA. Verify with `actionlint`
- [ ] 3.2 Delete `.goreleaser.yaml` and the GoReleaser block in `.gitignore`; verify no `goreleaser` references remain outside `openspec/`
- [ ] 3.3 Update `README.md`: remove the Binary install section and GoReleaser prerequisite, update the CI table (`docker-build`, `release`, `image`). Verify with markdown lint on the file
- [ ] 3.4 Update the Purpose of `openspec/specs/ci-release/spec.md` to drop binaries/checksums/GoReleaser; verify `openspec validate release-without-goreleaser --strict`
