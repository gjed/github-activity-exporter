## ADDED Requirements

### Requirement: PR count by state

The system SHALL record `github.pr.count` as an UpDownCounter broken down by repo and state (open, merged, closed-without-merge).

#### Scenario: Closed-without-merge counted separately from merged

- **WHEN** a PR was closed but not merged
- **THEN** it increments `github.pr.count` with `github.pr.state = "closed"` (not `"merged"`)

#### Scenario: Lookback window applied for closed/merged

- **WHEN** collecting PR state
- **THEN** closed and merged PRs are counted only within the configured `lookback_days` window (default: 30 days)

### Requirement: PR age gauge (oldest open)

The system SHALL record `github.pr.age` as a Gauge with the age in seconds of the oldest currently open PR per repo.

#### Scenario: Oldest open PR age reported

- **WHEN** a repo has 5 open PRs created at different times
- **THEN** `github.pr.age` reflects the age of the one created earliest

#### Scenario: Zero when no open PRs

- **WHEN** a repo has no open PRs matching the configured filters
- **THEN** `github.pr.age` is 0 for that repo

### Requirement: PR close duration histogram

The system SHALL record `github.pr.close.duration` as a Histogram (seconds) for PRs that were closed or merged within the lookback window.

#### Scenario: Merged PR contributes to histogram

- **WHEN** a PR was merged 48 hours after creation
- **THEN** it is recorded in the `172800`-second range of `github.pr.close.duration`

#### Scenario: Default histogram buckets

- **WHEN** no custom buckets are configured
- **THEN** default bucket boundaries are: 3600, 14400, 28800, 86400, 172800, 259200, 604800, 1209600, 2592000 seconds (1h to 30d)

### Requirement: Automerge counter

The system SHALL record `github.pr.automerged` as a Counter incremented for each PR merged with no approved human review, within the lookback window.

#### Scenario: Merged-no-review PR counted as automerged

- **WHEN** a PR was merged and its review decision was never `APPROVED`
- **THEN** `github.pr.automerged` is incremented for that repo

### Requirement: PR count by label

The system SHALL record `github.pr.count` broken down by label for each PR, enabling update-type analysis.

#### Scenario: PR counted per label

- **WHEN** a PR has labels `["renovate", "major"]`
- **THEN** it contributes to `github.pr.count` once per label with `github.pr.label` attribute set

### Requirement: PR review status gauge

The system SHALL record `github.pr.review_status` as a Gauge counting open PRs by their current review decision state.

#### Scenario: Approved PRs counted

- **WHEN** 3 open PRs have an `APPROVED` review decision
- **THEN** `github.pr.review_status{github.pr.review_status="approved"}` = 3
