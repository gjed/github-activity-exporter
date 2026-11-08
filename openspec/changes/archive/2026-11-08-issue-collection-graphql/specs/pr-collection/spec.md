# Spec Delta

## ADDED Requirements

### Requirement: Renovate dashboard lookup

The dashboard collector SHALL find a repo's Dependency Dashboard with a query for open issues created by the configured bot login, and SHALL NOT page through pull requests. The dashboard is the open issue titled "Dependency Dashboard" whose author is the configured login.

#### Scenario: Repo with many open PRs

- **WHEN** a repo has many open Renovate pull requests and one Dependency Dashboard issue
- **THEN** the dashboard is found with one request

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
