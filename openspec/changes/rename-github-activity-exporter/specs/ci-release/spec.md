## MODIFIED Requirements

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
