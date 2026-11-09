# pr-collection Specification

## Purpose

Defines which pull requests a target selects and how the exporter turns them into open-state gauges and created/merged/closed event counters without truncation or double counting.

## Requirements

### Requirement: PR author filter

A target SHALL accept `filters.prs.authors`, a list of logins. When non-empty, only PRs whose author login matches one entry, case-insensitively, SHALL be selected. GitHub App bot authors SHALL be matched by their REST login with the `[bot]` suffix (for example `renovate[bot]`).

#### Scenario: Bot author selected

- **WHEN** `authors` is `[renovate[bot]]` and a PR is authored by the Renovate GitHub App
- **THEN** the PR is selected

#### Scenario: Other author dropped

- **WHEN** `authors` is `[renovate[bot]]` and a PR is authored by user `alice`
- **THEN** the PR is not selected

#### Scenario: Self-hosted user author selected

- **WHEN** `authors` is `[renovate-bot]` and a PR is authored by user `renovate-bot`
- **THEN** the PR is selected

### Requirement: PR head branch prefix filter

A target SHALL accept `filters.prs.head_branch_prefix`, a string. When non-empty, only PRs whose head branch name starts with it (case-sensitive) SHALL be selected.

#### Scenario: Matching branch selected

- **WHEN** `head_branch_prefix` is `renovate/` and a PR's head branch is `renovate/go-1.x`
- **THEN** the PR is selected

#### Scenario: Non-matching branch dropped

- **WHEN** `head_branch_prefix` is `renovate/` and a PR's head branch is `feature/login`
- **THEN** the PR is not selected

### Requirement: Combined PR filters

All configured PR filters (`authors`, `head_branch_prefix`, `include_labels`, `exclude_labels`, `states`) SHALL be applied together: a PR is selected only if it passes every non-empty filter. The selection SHALL apply to every PR metric, including event counters. `states` SHALL be evaluated against the PR's current state.

#### Scenario: Label filter selects

- **WHEN** `include_labels` is `[renovate]` and a PR carries label `Renovate`
- **THEN** the PR is selected

#### Scenario: Exclude label wins

- **WHEN** `include_labels` is `[renovate]`, `exclude_labels` is `[wip]` and a PR carries both labels
- **THEN** the PR is not selected

#### Scenario: Filter applies to event counters

- **WHEN** `authors` is `[renovate[bot]]` and a PR by `alice` is merged within the event window
- **THEN** `github.pr.merged` is not incremented for it

### Requirement: Open PR state gauges without cap

Each cycle SHALL read every open PR of a repo (drafts included), with no per-repo limit, and export `github.pr.count` for states `open` and `draft` only, `github.pr.age` and `github.pr.review_status` from the selected PRs. `github.pr.count` SHALL NOT report `closed` or `merged`.

#### Scenario: Repo with more than 500 open PRs

- **WHEN** a repo has 1200 open selected PRs
- **THEN** `github.pr.count{github.pr.state="open"}` is 1200

#### Scenario: No closed or merged totals

- **WHEN** a repo has closed and merged PRs
- **THEN** no `github.pr.count` data point carries `github.pr.state` `closed` or `merged`

### Requirement: PR event counters

The exporter SHALL increment counter `github.pr.created` once per selected PR created in the event window, `github.pr.merged` once per selected PR merged in it, and `github.pr.closed` once per selected PR closed without merge in it, each with attributes `exporter.target`, `github.org` and `github.repo`. Counters SHALL reset only on restart.

#### Scenario: Merge counted

- **WHEN** a selected PR is merged inside the event window
- **THEN** `github.pr.merged` for its repo increases by 1 and `github.pr.closed` does not change

#### Scenario: Close without merge counted

- **WHEN** a selected PR is closed without merge inside the event window
- **THEN** `github.pr.closed` for its repo increases by 1

#### Scenario: Created counted

- **WHEN** a selected PR is created inside the event window
- **THEN** `github.pr.created` for its repo increases by 1

### Requirement: Event window

For a repo, a cycle's event window SHALL be `(max(lookback cutoff, start of the repo's previous successful cycle), start of the current cycle]`, with the lookback cutoff 30 days before the current cycle start. PRs SHALL be read by last update, newest first, back to the previous cycle's window start, and pagination SHALL stop there. A repo's first successful cycle SHALL record no events.

#### Scenario: Pagination stops at window start

- **WHEN** the previous cycle's window started at T and a page ends with a PR last updated before T
- **THEN** no further page is requested for that repo

#### Scenario: Lookback caps the window

- **WHEN** the repo's previous successful cycle was 40 days ago
- **THEN** only events within the last 30 days are recorded

#### Scenario: First cycle records no events

- **WHEN** a repo is collected for the first time
- **THEN** `github.pr.created`, `github.pr.merged`, `github.pr.closed`, `github.pr.close.duration` and `github.pr.automerged` record no events for it

### Requirement: No double counting across cycles

Each PR event SHALL be recorded at most once across consecutive cycles, including events the previous cycle's scan already saw. An event missed because its PR was updated while the previous scan paged SHALL be recorded by the next cycle. Events of a cycle whose repo collection fails SHALL NOT be recorded, and the repo's window SHALL NOT advance, so they are recorded on the next successful cycle.

#### Scenario: Consecutive cycles

- **WHEN** a PR merged at time M is visible in two consecutive successful cycles
- **THEN** `github.pr.merged`, `github.pr.close.duration` and `github.pr.automerged` record it exactly once

#### Scenario: PR updated mid-scan

- **WHEN** a PR merged inside a cycle's window is updated while that cycle's scan pages and the scan does not reach it
- **THEN** the next successful cycle records its merge once

#### Scenario: Failed cycle retried

- **WHEN** a repo's collection fails partway through a cycle
- **THEN** no event from that cycle is recorded and the next successful cycle records them

### Requirement: Optional Renovate dashboard collector

A target SHALL accept `renovate_dashboard.enabled`, default `false`. The Renovate Dependency Dashboard collector SHALL run for a target only when it is `true`. `renovate.*` attributes SHALL appear only on dashboard metrics.

#### Scenario: Disabled by default

- **WHEN** a target does not set `renovate_dashboard`
- **THEN** no dashboard issue is requested and no `renovate.dashboard.*` metric is emitted for it

#### Scenario: Enabled

- **WHEN** a target sets `renovate_dashboard.enabled: true`
- **THEN** `renovate.dashboard.queue` is emitted for its repos with a dashboard issue

### Requirement: Renovate dashboard parse error

`renovate.dashboard.parse_error` SHALL be 1 only when the dashboard issue body is not a Renovate Dependency Dashboard, and 0 otherwise. A body is a Renovate Dependency Dashboard when it contains Renovate's default header, the "no open or pending branches" notice, a `## Detected Dependencies` heading, or a branch checkbox.

#### Scenario: Dashboard with no open or pending branches

- **WHEN** the body contains "This repository currently has no open or pending branches." and no tracked section
- **THEN** `renovate.dashboard.parse_error` is 0 and `renovate.dashboard.queue` is 0 for every tracked section

#### Scenario: Dashboard with only untracked sections

- **WHEN** the body is a Renovate dashboard whose only sections are untracked, such as "PR Edited (Blocked)" or "Config Migration Needed"
- **THEN** `renovate.dashboard.parse_error` is 0 and `renovate.dashboard.queue` is 0 for every tracked section

#### Scenario: Body is not a Renovate dashboard

- **WHEN** the body contains none of the Renovate dashboard markers
- **THEN** `renovate.dashboard.parse_error` is 1 and no `renovate.dashboard.queue` value is emitted for the repo

### Requirement: Renovate dashboard queue counts branches

`renovate.dashboard.queue` SHALL count, per tracked section, only checkboxes that reference a branch (`<!-- <type>-branch=<name> -->`), checked or not. Bulk-action checkboxes and any other checkbox without a branch reference SHALL NOT be counted.

#### Scenario: Section with a bulk-action checkbox

- **WHEN** the Rate-Limited section lists 11 branch checkboxes and the `create-all-rate-limited-prs` checkbox
- **THEN** `renovate.dashboard.queue{renovate.dashboard.section="rate-limited"}` is 11

#### Scenario: Branches under category subheadings

- **WHEN** a tracked section groups its branch checkboxes under `###` category subheadings
- **THEN** all branch checkboxes under those subheadings count toward the enclosing section

### Requirement: Renovate dashboard lookup

The dashboard collector SHALL find a repo's Dependency Dashboard with a query for open issues created by the configured bot login, and SHALL NOT page through pull requests. The dashboard is the open issue titled "Dependency Dashboard" whose author is the configured login.

#### Scenario: Repo with many open PRs

- **WHEN** a repo has many open Renovate pull requests and one Dependency Dashboard issue
- **THEN** the dashboard is found within the repo's batch request, with no further request for the repo

#### Scenario: No dashboard issue

- **WHEN** the bot has no open issue titled "Dependency Dashboard" in the repo
- **THEN** no `renovate.dashboard.*` metric is emitted for the repo and no error is recorded

### Requirement: Renovate bot login format

The configured bot login SHALL use the REST form, where a GitHub App bot is `<app>[bot]`, and the default SHALL stay `renovate[bot]`. A bot author returned without the `[bot]` suffix SHALL match the configured `<app>[bot]` login. User accounts SHALL match by login, case-insensitively.

#### Scenario: GitHub App bot

- **WHEN** the configured login is `renovate[bot]` and the dashboard author is the bot account reported as `renovate`
- **THEN** the dashboard is found

#### Scenario: User account bot

- **WHEN** the configured login is `ZxBot` and the dashboard author is the user `ZxBot`
- **THEN** the dashboard is found
