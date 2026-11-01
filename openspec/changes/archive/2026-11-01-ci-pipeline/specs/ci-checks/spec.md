## ADDED Requirements

### Requirement: Lint check on every PR

The system SHALL run `golangci-lint` on every pull request and fail the CI job if any linter error is reported.

#### Scenario: Lint failure blocks merge

- **WHEN** a PR introduces code that fails a `golangci-lint` rule
- **THEN** the `lint` CI job fails and the PR cannot be merged

### Requirement: Unit tests with coverage on every PR

The system SHALL run `go test ./...` on every PR and report coverage. The job SHALL fail if tests fail.

#### Scenario: Test failure blocks merge

- **WHEN** a PR introduces a failing unit test
- **THEN** the `test` CI job fails

#### Scenario: Coverage reported as PR annotation

- **WHEN** tests pass
- **THEN** coverage percentage is reported as a check annotation (not a hard gate for MVP)

### Requirement: Build verification on every PR

The system SHALL run `go build ./...` on every PR to verify the project compiles for `linux/amd64`.

#### Scenario: Build failure blocks merge

- **WHEN** a PR introduces a compilation error
- **THEN** the `build` CI job fails

### Requirement: Weaver schema check on every PR

The system SHALL run `weaver registry check -r registry/` on every PR and fail if the registry is invalid.

#### Scenario: Invalid schema fails CI

- **WHEN** a PR modifies `registry/` and introduces a schema error
- **THEN** the `weaver-check` CI job fails

#### Scenario: Schema check runs even without registry changes

- **WHEN** any file is changed in a PR
- **THEN** the Weaver schema check still runs (schema validity is always verified)

### Requirement: Stale generated code check on every PR

The system SHALL verify that generated files (`internal/semconv/`, `dashboards/`) are up to date by running `make generate` and checking for uncommitted changes.

#### Scenario: Stale semconv fails CI

- **WHEN** a PR modifies `registry/` but does not regenerate `internal/semconv/`
- **THEN** the `check-generated` CI job fails with a diff showing the stale files

### Requirement: CI runs on ubuntu-latest

All CI check jobs SHALL run on `ubuntu-latest` GitHub-hosted runners.

#### Scenario: No self-hosted runner dependency for checks

- **WHEN** a PR is opened
- **THEN** all check jobs start without requiring a self-hosted runner
