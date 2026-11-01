## ADDED Requirements

### Requirement: Integration test job in CI

The system SHALL run integration tests as a separate CI job on PRs targeting `main`, starting the exporter with a mock configuration and verifying end-to-end behavior.

#### Scenario: Integration tests run on PRs to main

- **WHEN** a PR is opened or updated targeting `main`
- **THEN** the `integration-test` CI job runs

#### Scenario: Integration tests do not run on every feature branch push

- **WHEN** a push is made to a branch not targeting `main`
- **THEN** the `integration-test` job is skipped

### Requirement: Weaver live-check in integration tests

The integration test job SHALL run `weaver registry live-check` against the running exporter and fail if any schema violations are detected.

#### Scenario: Live-check passes on clean code

- **WHEN** all emitted metrics conform to the registry schema
- **THEN** `weaver registry live-check` exits with code 0 and the integration job passes

#### Scenario: Live-check failure fails CI

- **WHEN** a PR causes the exporter to emit a metric that violates the schema
- **THEN** `weaver registry live-check` exits non-zero and the integration job fails

### Requirement: Integration tests use no real GitHub credentials

The integration test job SHALL use a mock GitHub API server so that no real GitHub token is required in CI.

#### Scenario: No secrets required for integration tests

- **WHEN** the integration test job runs on a fork PR
- **THEN** the job completes successfully without needing `GITHUB_TOKEN` or any other secret

### Requirement: Telemetry registry validation workflow

The system SHALL have a dedicated `registry-validate.yml` workflow that runs `weaver registry check` on every push and PR, independently of the main CI workflow, so schema validation is always visible as a separate status check.

#### Scenario: Registry validation is a distinct status check

- **WHEN** a PR is opened
- **THEN** `registry-validate` appears as an independent required status check alongside `lint`, `test`, `build`

#### Scenario: Registry-only change triggers validation

- **WHEN** a PR modifies only files in `registry/`
- **THEN** the `registry-validate` job runs and reports pass/fail

#### Scenario: Weaver emit validates example telemetry

- **WHEN** the registry validation job runs
- **THEN** it also runs `weaver registry emit -r registry/` to verify the schema can produce valid example telemetry (smoke test)

### Requirement: Unit tests run on every PR and push to main

The system SHALL run `go test -race ./...` on every pull request and on every push to `main`.

#### Scenario: Unit test failure blocks merge

- **WHEN** a PR introduces a failing unit test
- **THEN** the `test` CI job fails and the PR cannot be merged

#### Scenario: Race detector enabled

- **WHEN** unit tests run in CI
- **THEN** the `-race` flag is always passed to detect data races

#### Scenario: Test results uploaded as artifact

- **WHEN** unit tests complete (pass or fail)
- **THEN** a JUnit XML report is uploaded as a CI artifact for inspection

### Requirement: Test coverage reported on PRs

The system SHALL report test coverage percentage as a PR check annotation. Coverage below a configured threshold SHALL NOT block merge in MVP but SHALL be visible.

#### Scenario: Coverage percentage visible on PR

- **WHEN** unit tests pass
- **THEN** the coverage percentage is posted as a comment or check annotation on the PR
