## MODIFIED Requirements

### Requirement: Semantic-release drives version and release on push to main

The system SHALL use `semantic-release` to automatically determine the next version, generate release notes, create a git tag, and trigger GoReleaser — all triggered by a push to `main`, not by a manual tag push. The release SHALL NOT commit to `main`.

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

## REMOVED Requirements

### Requirement: CHANGELOG.md maintained by semantic-release

**Reason**: semantic-release recommends against committing during a release, and the GitHub Release carries the same notes.
**Migration**: Read release notes on the GitHub Release page; they carry the same content.
