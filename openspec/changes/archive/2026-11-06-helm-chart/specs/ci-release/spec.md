# Spec Delta

## ADDED Requirements

### Requirement: Helm chart published to GHCR

The system SHALL package the Helm chart with `version` and `appVersion` equal to the release version and push it to `oci://ghcr.io/gjed/charts/github-activity-exporter` when a GitHub Release is published. Re-running the chart workflow from a version tag SHALL republish that version.

#### Scenario: Chart pushed on release

- **WHEN** release `vX.Y.Z` is published
- **THEN** `helm pull oci://ghcr.io/gjed/charts/github-activity-exporter --version X.Y.Z` returns a chart with `appVersion` `X.Y.Z`

#### Scenario: Chart failure does not orphan the release

- **WHEN** the chart publish fails after release `vX.Y.Z` is published
- **THEN** the tag, the GitHub Release and the image remain, and re-running only the chart workflow publishes the chart
