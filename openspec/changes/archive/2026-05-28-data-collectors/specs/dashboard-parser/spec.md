## ADDED Requirements

### Requirement: Dependency Dashboard identification

The system SHALL identify the Renovate Dependency Dashboard issue in a repo by matching title `"Dependency Dashboard"` and author matching the configured Renovate bot account name.

#### Scenario: Dashboard identified by title and author

- **WHEN** a repo has multiple issues with similar titles
- **THEN** only the issue authored by the configured Renovate bot account and titled exactly `"Dependency Dashboard"` is used for parsing

#### Scenario: No dashboard issue is not an error

- **WHEN** no issue matches the dashboard criteria in a repo
- **THEN** no `renovate.dashboard.*` metrics are emitted for that repo (silent absence, not an error)

### Requirement: Queue section parsing

The system SHALL parse the Dependency Dashboard issue body and extract entry counts from the following Markdown sections: `Awaiting Schedule`, `Rate-Limited`, `Pending Approval`, `Open`.

#### Scenario: Awaiting Schedule count extracted

- **WHEN** the dashboard body contains `## Awaiting Schedule` followed by checkbox list items
- **THEN** `renovate.dashboard.queue{renovate.dashboard.section="awaiting_schedule"}` = count of list items in that section

#### Scenario: Rate-Limited count extracted

- **WHEN** the dashboard body contains `## Rate-Limited` section
- **THEN** `renovate.dashboard.queue{renovate.dashboard.section="rate_limited"}` = count of list items

#### Scenario: Pending Approval count extracted

- **WHEN** the dashboard body contains `## Pending Approval` section
- **THEN** `renovate.dashboard.queue{renovate.dashboard.section="pending_approval"}` = count of list items

#### Scenario: Open count extracted

- **WHEN** the dashboard body contains `## Open` section
- **THEN** `renovate.dashboard.queue{renovate.dashboard.section="open"}` = count of list items

### Requirement: Parse error metric

The system SHALL emit a parse error metric when the dashboard issue body does not match the expected section format.

#### Scenario: Unknown format triggers error metric

- **WHEN** the dashboard issue exists but contains none of the expected `## ` section headers
- **THEN** `renovate.dashboard.parse_error{github.repo}` = 1

#### Scenario: Known format clears error metric

- **WHEN** the dashboard is successfully parsed
- **THEN** `renovate.dashboard.parse_error{github.repo}` = 0

### Requirement: Dashboard excluded from issue count metrics

The system SHALL NOT count the Dependency Dashboard issue in `github.issue.count` metrics — it is consumed only by the dashboard parser.

#### Scenario: Dashboard excluded despite matching state/label filters

- **WHEN** the Dependency Dashboard issue is open and no title filter is configured
- **THEN** it is still excluded from `github.issue.count` because it is identified as the dashboard and handled separately
