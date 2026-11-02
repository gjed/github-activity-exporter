# ci-release Specification

## Purpose

Automate releases: version, tag and GitHub Release with notes from Conventional Commits, and a multi-arch GHCR image built when the release is published.

## Requirements

### Requirement: Semantic-release drives version and release on push to main

The system SHALL use `semantic-release` to automatically determine the next version, generate release notes, create a git tag, and create the GitHub Release with those notes — all triggered by a push to `main`, not by a manual tag push. The release SHALL NOT commit to `main`.

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

#### Scenario: Release does not commit to main

- **WHEN** a release is published
- **THEN** the only ref the release pushes is the version tag, and `main` has no new commit

#### Scenario: GitHub Release carries the generated notes

- **WHEN** semantic-release publishes version `X.Y.Z`
- **THEN** a GitHub Release for tag `vX.Y.Z` exists with the conventional-commits release notes and no attached binary assets

### Requirement: Docker image published to GHCR

The system SHALL build and push a multi-arch Docker image to `ghcr.io/gjed/github-activity-exporter` when a GitHub Release is published, tagged with the full version, the major.minor version, the major version and `latest`.

#### Scenario: Image pushed on release

- **WHEN** release `vX.Y.Z` is published
- **THEN** `ghcr.io/gjed/github-activity-exporter:X.Y.Z`, `:X.Y`, `:X` and `:latest` are available on GHCR

#### Scenario: Image is multi-arch

- **WHEN** the image is pulled
- **THEN** it runs natively on both `linux/amd64` and `linux/arm64`

#### Scenario: Image reports the release version

- **WHEN** `docker run ghcr.io/gjed/github-activity-exporter:X.Y.Z --version` is executed
- **THEN** the output contains `X.Y.Z`

#### Scenario: Image failure does not orphan the release

- **WHEN** the image build fails after release `vX.Y.Z` is published
- **THEN** the tag and the GitHub Release remain, and re-running only the image workflow publishes the image
