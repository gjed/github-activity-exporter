# Spec Delta

## ADDED Requirements

### Requirement: Helm chart check on chart PRs

The system SHALL run `helm lint`, `helm template` and `kubeconform -strict` on the chart, with default values and with each example values file, on every pull request that changes `charts/`, `dashboards/github-activity.json` or the chart workflow. The check SHALL fail on any lint error, render error or schema violation.

#### Scenario: Invalid manifest blocks merge

- **WHEN** a PR changes a chart template so that a rendered manifest violates the Kubernetes schema
- **THEN** the chart check fails

#### Scenario: Unrelated PR skips the check

- **WHEN** a PR changes only Go code
- **THEN** the chart check does not run
