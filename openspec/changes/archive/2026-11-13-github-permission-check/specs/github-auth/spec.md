## ADDED Requirements

### Requirement: Required GitHub permissions

A target's credential SHALL need only the permissions of the calls its discovery mode and enabled collectors make. GitHub App and fine-grained PAT: Metadata, Pull requests and Issues, read. Classic PAT: `repo` scope. `GET /users/{owner}`, `GET /user` and public repos need no permission. The Dependency Dashboard collector needs Issues read, already required by the issue collector.

#### Scenario: GitHub App with the minimum permissions

- **WHEN** a GitHub App installation grants Metadata, Pull requests and Issues read-only and nothing else
- **THEN** discovery and every collector of the target succeed

#### Scenario: Fine-grained PAT with the minimum permissions

- **WHEN** a fine-grained PAT grants Metadata, Pull requests and Issues read-only on the monitored repos
- **THEN** discovery and every collector of the target succeed

#### Scenario: Classic PAT with repo scope

- **WHEN** a classic PAT has the `repo` scope and no other
- **THEN** discovery and every collector of the target succeed, private repos included

#### Scenario: Write access satisfies read

- **WHEN** a credential grants write access for a permission the exporter needs read access for
- **THEN** that permission counts as granted

### Requirement: Permission check on startup

On startup the system SHALL check each target's granted permissions against the required ones, after the credential check and before the target's first collection cycle, and exit with one error naming the target and every missing permission.

#### Scenario: GitHub App missing a permission

- **WHEN** a GitHub App installation does not grant Issues read
- **THEN** the exporter exits at startup with an error naming the target and `issues: read`

#### Scenario: GitHub App repository selection

- **WHEN** a GitHub App installation has repository selection `selected`
- **THEN** the exporter logs the target and the selection, and discovers only the selected repos

#### Scenario: Classic PAT missing the repo scope

- **WHEN** GitHub answers a target's token with an `X-OAuth-Scopes` header that does not contain `repo`
- **THEN** the exporter exits at startup with an error naming the target and the `repo` scope

#### Scenario: Fine-grained PAT missing a permission

- **WHEN** a target's token gets no `X-OAuth-Scopes` header and reading pull requests of a private repo the token can see is forbidden
- **THEN** the exporter exits at startup with an error naming the target and `pull_requests: read`

#### Scenario: Fine-grained PAT with public repos only

- **WHEN** a fine-grained PAT can see no private repo of a configured owner
- **THEN** the check passes for that owner, since every token can read public repos

#### Scenario: Several missing permissions

- **WHEN** a target's credential lacks both Pull requests and Issues read
- **THEN** one error names the target and both permissions

#### Scenario: Not ready before the check

- **WHEN** a target's permission check has not passed
- **THEN** `/readyz` reports the target's credential check as pending, and a failed check exits the exporter before any target becomes ready

### Requirement: Permission probes and rate limits

Permission probes SHALL use the target's GitHub client: they wait on a paused rate-limit pool and on a secondary rate limit pause like any request, are not counted against the target's collection concurrency, and record rate-limit state only for pools of the target's own credential.

#### Scenario: Probe during a secondary rate limit pause

- **WHEN** the target is paused after a secondary rate limit when the permission check starts
- **THEN** the probe is sent after the pause ends

#### Scenario: App installation permissions need no request

- **WHEN** the target authenticates as a GitHub App
- **THEN** the permissions are read from the installation token exchange the exporter already makes, and no request is sent with the App's JWT
