# ci-checks Specification

## Purpose

Gate every pull request and push to main on lint, unit tests with coverage, build, Weaver registry validation, and stale-codegen detection.

## Requirements

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

### Requirement: Helm chart check on chart PRs

The system SHALL run `helm lint`, `helm template` and `kubeconform -strict` on the chart, with default values and with each example values file, on every pull request that changes `charts/`, `dashboards/github-activity.json` or the chart workflow. The check SHALL fail on any lint error, render error or schema violation.

#### Scenario: Invalid manifest blocks merge

- **WHEN** a PR changes a chart template so that a rendered manifest violates the Kubernetes schema
- **THEN** the chart check fails

#### Scenario: Unrelated PR skips the check

- **WHEN** a PR changes only Go code
- **THEN** the chart check does not run

### Requirement: Dashboard queries verified end to end on every PR

The system SHALL run an end-to-end check on every pull request that feeds the exporter binary, run against a mock GitHub API with the Dependency Dashboard collector on, into Prometheus 3.x over OTLP (UTF-8 names, `NoTranslation`), then runs every query in the generated dashboard with variables substituted. It SHALL fail when any query errors or returns an empty result.

#### Scenario: Every panel query returns data

- **WHEN** the e2e check runs against the generated dashboard
- **THEN** every panel target and every variable query returns a non-empty result without error, and the job passes

#### Scenario: Broken query fails CI

- **WHEN** a dashboard query uses a name, suffix or shape the backend does not have (for example an unquoted dotted name or a histogram without `_bucket`)
- **THEN** the e2e check fails and names the panel, the query and the Prometheus response

#### Scenario: Make target

- **WHEN** a developer runs `make test-e2e` with Docker available
- **THEN** the same check runs locally
