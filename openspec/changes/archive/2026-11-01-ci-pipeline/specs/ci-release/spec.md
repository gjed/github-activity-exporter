## ADDED Requirements

### Requirement: Semantic-release drives version and release on push to main

The system SHALL use `semantic-release` to automatically determine the next version, generate release notes, update `CHANGELOG.md`, create a git tag, and trigger GoReleaser — all triggered by a push to `main`, not by a manual tag push.

#### Scenario: feat commit triggers minor release

- **WHEN** a commit with `feat:` prefix is merged to `main`
- **THEN** semantic-release bumps the minor version, creates a tag, and publishes a release

#### Scenario: fix commit triggers patch release

- **WHEN** a commit with `fix:` prefix is merged to `main`
- **THEN** semantic-release bumps the patch version

#### Scenario: BREAKING CHANGE triggers major release

- **WHEN** a commit footer contains `BREAKING CHANGE:` or uses `feat!:`
- **THEN** semantic-release bumps the major version

#### Scenario: chore/docs/test commits produce no release

- **WHEN** only `chore:`, `docs:`, or `test:` commits are merged to `main`
- **THEN** semantic-release determines no release is necessary and exits without creating a tag

### Requirement: semantic-release invokes GoReleaser for artifact production

The system SHALL use `@semantic-release/exec` to invoke GoReleaser with the version and release notes determined by semantic-release.

#### Scenario: GoReleaser receives version from semantic-release

- **WHEN** semantic-release determines the next version
- **THEN** it sets `GORELEASER_CURRENT_TAG` and calls `goreleaser release --clean --release-notes /tmp/release-notes.md`

### Requirement: CHANGELOG.md maintained by semantic-release

The system SHALL use `@semantic-release/changelog` to update `CHANGELOG.md` and commit it back to `main` as part of the release process.

#### Scenario: CHANGELOG updated on release

- **WHEN** a release is published
- **THEN** `CHANGELOG.md` is updated with the new version's entries and committed with message `chore(release): <version>`

### Requirement: Multi-arch binary builds via GoReleaser

The system SHALL produce release binaries for at minimum: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`.

#### Scenario: All target platform binaries present in release

- **WHEN** the release workflow completes
- **THEN** the GitHub Release contains archives for all four platform/arch combinations

### Requirement: Checksums file

The system SHALL generate a `checksums.txt` file listing SHA256 hashes for all release artifacts.

#### Scenario: Checksums file attached to release

- **WHEN** the release workflow completes
- **THEN** a `checksums.txt` file is attached to the GitHub Release

### Requirement: Docker image published to GHCR

The system SHALL build and push a multi-arch Docker image to `ghcr.io/gjed/renovate-exporter` tagged with the release version and `latest`.

#### Scenario: Image pushed on release

- **WHEN** a release is published
- **THEN** `ghcr.io/gjed/renovate-exporter:<version>` and `ghcr.io/gjed/renovate-exporter:latest` are available on GHCR

#### Scenario: Image is multi-arch

- **WHEN** the image is pulled
- **THEN** it runs natively on both `linux/amd64` and `linux/arm64`
