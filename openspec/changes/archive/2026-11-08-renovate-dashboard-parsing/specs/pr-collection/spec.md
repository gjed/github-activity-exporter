# Spec Delta

## ADDED Requirements

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
