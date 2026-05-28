## ADDED Requirements

### Requirement: Issue count by state

The system SHALL record `github.issue.count` as an UpDownCounter broken down by repo and state (open, closed).

#### Scenario: Open issues counted per repo

- **WHEN** a repo has 5 open issues
- **THEN** `github.issue.count{github.issue.state="open"}` = 5 for that repo

### Requirement: Issue count by label

The system SHALL record `github.issue.count` broken down by label when issues carry labels.

#### Scenario: Issue counted per label

- **WHEN** an issue has labels `["bug", "priority-high"]`
- **THEN** it contributes to `github.issue.count` once per label with `github.issue.label` attribute

### Requirement: Issue age gauge (oldest open)

The system SHALL record `github.issue.age` as a Gauge with the age in seconds of the oldest currently open issue per repo (after filters applied).

#### Scenario: Oldest open issue age reported

- **WHEN** a repo has open issues at various ages
- **THEN** `github.issue.age` reflects the age of the earliest-created open issue
