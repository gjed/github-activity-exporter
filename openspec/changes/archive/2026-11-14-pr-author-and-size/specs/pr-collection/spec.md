# Spec Delta: pr-collection

## ADDED Requirements

### Requirement: PR size histograms

The exporter SHALL record histograms `github.pr.lines.added` and `github.pr.lines.removed`, unit `{line}`, with the PR's additions and deletions, once per selected PR merged or closed in the event window, on the same events and with the same dedup as `github.pr.close.duration`, with attributes `exporter.target`, `github.org` and `github.repo`. Recording SHALL NOT add a GraphQL request.

#### Scenario: Merge recorded

- **WHEN** a selected PR with 120 additions and 30 deletions is merged inside the event window
- **THEN** `github.pr.lines.added` records 120 and `github.pr.lines.removed` records 30 for its repo, once

#### Scenario: Size recorded once across cycles

- **WHEN** a PR merged at time M is visible in two consecutive successful cycles
- **THEN** each size histogram records it exactly once

#### Scenario: Concurrent repos

- **WHEN** a target collects its repos with concurrency greater than 1
- **THEN** each merged or closed PR is recorded once, as with concurrency 1

#### Scenario: First cycle records no sizes

- **WHEN** a repo is collected for the first time
- **THEN** neither size histogram records a value for it

#### Scenario: No extra request

- **WHEN** a cycle records PR sizes
- **THEN** the number of GraphQL requests per repo is the same as without the size histograms

### Requirement: PR author setting

The system SHALL read the boolean `collection.pr_author` at the top level as the default for every target, and `collection.pr_author` on a target as its override. The default SHALL be `false`. A non-boolean value SHALL fail startup.

#### Scenario: Default off

- **WHEN** neither the top level nor the target sets `collection.pr_author`
- **THEN** the target records no `github.pr.author` attribute

#### Scenario: Target override

- **WHEN** the top level sets `collection.pr_author: true` and a target sets `collection.pr_author: false`
- **THEN** that target records no `github.pr.author` attribute and the other targets do

#### Scenario: Invalid value

- **WHEN** `collection.pr_author` is set to `maybe`
- **THEN** startup fails with an error naming `pr_author`

### Requirement: PR author attribute

When `collection.pr_author` is on for a target, `github.pr.lines.added`, `github.pr.lines.removed` and `github.pr.close.duration` SHALL carry `github.pr.author`, the PR author's login exactly as the GraphQL API returns it. A PR without an author SHALL be recorded without the attribute. When off, the three histograms SHALL carry only `exporter.target`, `github.org` and `github.repo`.

#### Scenario: Author recorded

- **WHEN** the setting is on and user `alice` merges a PR
- **THEN** the three histograms record it with `github.pr.author="alice"`

#### Scenario: Bot login verbatim

- **WHEN** the setting is on and the Renovate GitHub App authors a merged PR
- **THEN** the histograms record `github.pr.author="renovate"`, while the `authors` filter still matches it as `renovate[bot]`

#### Scenario: Deleted author

- **WHEN** the setting is on and a merged PR's author account was deleted
- **THEN** the histograms record it without `github.pr.author`

#### Scenario: Setting off

- **WHEN** the setting is off
- **THEN** no metric carries `github.pr.author` and every pre-existing PR metric keeps its attributes and values
