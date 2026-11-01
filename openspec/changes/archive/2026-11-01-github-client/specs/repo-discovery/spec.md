## ADDED Requirements

### Requirement: Org autodiscovery

The system SHALL automatically discover all repositories within a configured GitHub organization without requiring a manual list.

#### Scenario: All non-archived non-fork repos discovered

- **WHEN** a target has an org configured with no explicit repo list
- **THEN** the exporter discovers all non-archived, non-forked repositories in that org on startup

#### Scenario: Include filter narrows discovery

- **WHEN** `orgs[].include_repos` contains glob patterns (e.g., `carbonio-*`)
- **THEN** only repositories whose names match at least one pattern are monitored

#### Scenario: Exclude filter removes repos

- **WHEN** `orgs[].exclude_repos` contains glob patterns (e.g., `*-i18n`, `archived-*`)
- **THEN** repositories matching any exclude pattern are omitted from the monitored set

#### Scenario: Include and exclude applied together

- **WHEN** both include and exclude patterns are configured
- **THEN** include is applied first, then exclude is applied to the result

### Requirement: Explicit repo list

The system SHALL support monitoring a manually specified list of repositories identified by `owner/name`, bypassing autodiscovery.

#### Scenario: Only listed repos monitored

- **WHEN** `repos` list is configured with `["zextras/carbonio-files-ce", "zextras/carbonio-auth"]`
- **THEN** exactly those two repositories are monitored, regardless of org membership

### Requirement: Multi-org per target

The system SHALL allow a single target to monitor multiple organizations, each with its own include/exclude filters.

#### Scenario: Two orgs in one target

- **WHEN** a target lists two orgs with different exclude patterns
- **THEN** repos from both orgs are discovered and monitored independently

### Requirement: Archived and fork exclusion

The system SHALL exclude archived and forked repositories from autodiscovery by default.

#### Scenario: Archived repos excluded

- **WHEN** autodiscovery runs and `include_archived` is not set
- **THEN** archived repositories are not included

#### Scenario: Archived repos opt-in

- **WHEN** `include_archived: true` is set for an org
- **THEN** archived repositories are included in the monitored set

### Requirement: Incremental refresh

The system SHALL periodically refresh the discovered repository list to pick up newly created repos.

#### Scenario: New repo appears after refresh

- **WHEN** a new repository is created in a monitored org and one refresh interval elapses
- **THEN** the new repo is included in the next collection cycle

#### Scenario: Default refresh interval

- **WHEN** `discovery.refresh_interval` is not configured
- **THEN** the repo list is refreshed every 60 minutes

### Requirement: Discovery logged at startup

The system SHALL log the final list of monitored repositories at `info` level on startup and after each refresh.

#### Scenario: Repo list logged

- **WHEN** discovery completes
- **THEN** a log line lists all discovered repos for each target (or a count if over 20 repos)
