## ADDED Requirements

### Requirement: Label-based PR include filter

The system SHALL include only PRs carrying at least one of the configured include labels when `filters.prs.include_labels` is set.

#### Scenario: Only renovate-labeled PRs included

- **WHEN** `filters.prs.include_labels: ["renovate"]` is configured
- **THEN** PRs without the `renovate` label are excluded from all PR metrics

### Requirement: Label-based PR exclude filter

The system SHALL exclude PRs carrying any of the configured exclude labels.

#### Scenario: Labeled PRs excluded

- **WHEN** `filters.prs.exclude_labels: ["wip"]` is configured
- **THEN** PRs with the `wip` label are omitted from all PR metrics

### Requirement: Issue title pattern exclusion

The system SHALL exclude issues whose titles match any configured regex pattern.

#### Scenario: Dependency Dashboard excluded from issue count

- **WHEN** `filters.issues.exclude_title_patterns: ["^Dependency Dashboard$"]` is configured
- **THEN** the Dependency Dashboard issue is not counted in `github.issue.count` (but IS still fetched for dashboard parsing)

### Requirement: State-based filtering

The system SHALL support filtering PRs and issues by state.

#### Scenario: Only open PRs monitored

- **WHEN** `filters.prs.states: ["open"]` is configured
- **THEN** closed and merged PRs are excluded from all PR metrics

#### Scenario: Default includes all states

- **WHEN** no state filter is configured
- **THEN** PRs and issues in all states are included

### Requirement: Per-target filter independence

Filters SHALL be scoped to the target they are configured on.

#### Scenario: Different targets have different filters

- **WHEN** target A has `include_labels: ["renovate"]` and target B has no label filter
- **THEN** target A metrics include only renovate-labeled PRs; target B includes all PRs
