# issue-collection Specification

## Purpose

Defines how the exporter reads a repository's issues and turns them into `github.issue.count` and `github.issue.age`, honouring the issue filters, without fetching pull requests.

## Requirements

### Requirement: Issues only

Issue collection SHALL request issues only. It SHALL NOT fetch pull requests, so its cost per repo SHALL NOT grow with the repo's pull request history.

#### Scenario: Repo with many PRs and few issues

- **WHEN** a repo has thousands of pull requests and a handful of issues, and no title pattern is configured
- **THEN** the repo is read within its batch's request, plus one request per further 100 repo labels, and no pull request is returned by any request

### Requirement: Issue counts and age

`github.issue.count` SHALL report, per repo, the number of issues per state and per label, counted over issues that pass the issue filters. `github.issue.age` SHALL report the age of the oldest open issue that passes the filters. A state or label with no matching issue SHALL NOT be emitted.

#### Scenario: Same values as the REST listing

- **WHEN** the collector runs against a repo
- **THEN** every `github.issue.count` and `github.issue.age` value equals the value computed from listing all the repo's issues and counting them

### Requirement: Issue state filter

When `filters.issues.states` is set, only issues in those states SHALL be counted, for both state and label counts, and `github.issue.age` SHALL be emitted only when `open` is among them.

#### Scenario: Closed only

- **WHEN** a target sets `filters.issues.states: [closed]`
- **THEN** no `github.issue.count{github.issue.state="open"}` and no `github.issue.age` is emitted, and label counts include closed issues only

### Requirement: Issue title exclusion

When `filters.issues.exclude_title_patterns` is set, issues whose title matches any pattern SHALL be excluded from every issue count and from `github.issue.age`.

#### Scenario: Excluded oldest issue

- **WHEN** the oldest open issue's title matches an exclusion pattern
- **THEN** it is not counted and `github.issue.age` reports the oldest open issue that does not match
