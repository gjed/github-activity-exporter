## ADDED Requirements

### Requirement: Owner type detection

The system SHALL determine whether each owner listed under `orgs` is an organization or a user account before listing its repositories, and SHALL reuse that result for the rest of the process lifetime.

#### Scenario: Organization owner

- **WHEN** an `orgs` entry names an organization
- **THEN** its repositories are discovered as for any organization

#### Scenario: Type looked up once

- **WHEN** discovery refreshes several times for the same owner
- **THEN** the owner type is requested from GitHub only once

#### Scenario: Unknown owner

- **WHEN** an `orgs` entry names an owner that GitHub reports as not found
- **THEN** discovery fails with an error naming the target and the owner, and the lookup is retried on the next refresh

### Requirement: User account autodiscovery

The system SHALL discover the repositories owned by a user account listed under `orgs`, applying `include_repos`, `exclude_repos`, `include_archived` and fork exclusion as for organizations.

#### Scenario: PAT owned by the user

- **WHEN** an `orgs` entry names the user that owns the target's PAT
- **THEN** the user's public and private repositories are discovered

#### Scenario: PAT of another identity

- **WHEN** an `orgs` entry names a user other than the PAT owner
- **THEN** only that user's public repositories are discovered

#### Scenario: GitHub App installed on the user account

- **WHEN** the target authenticates as a GitHub App installation and an `orgs` entry names a user account
- **THEN** the repositories of that installation owned by the user are discovered

#### Scenario: Filters apply to user accounts

- **WHEN** a user account entry sets `include_repos`, `exclude_repos` or `include_archived`
- **THEN** they narrow or widen the discovered set exactly as for an organization
