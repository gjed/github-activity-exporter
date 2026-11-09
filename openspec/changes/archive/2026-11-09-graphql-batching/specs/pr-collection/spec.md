## MODIFIED Requirements

### Requirement: Renovate dashboard lookup

The dashboard collector SHALL find a repo's Dependency Dashboard with a query for open issues created by the configured bot login, and SHALL NOT page through pull requests. The dashboard is the open issue titled "Dependency Dashboard" whose author is the configured login.

#### Scenario: Repo with many open PRs

- **WHEN** a repo has many open Renovate pull requests and one Dependency Dashboard issue
- **THEN** the dashboard is found within the repo's batch request, with no further request for the repo

#### Scenario: No dashboard issue

- **WHEN** the bot has no open issue titled "Dependency Dashboard" in the repo
- **THEN** no `renovate.dashboard.*` metric is emitted for the repo and no error is recorded
