# Tasks

## 1. Registry

- [ ] 1.1 Add counters `github.pr.created`, `github.pr.merged`, `github.pr.closed` (unit `{pr}`, `annotations.prometheus` type `counter`, required `exporter.target`, `github.org`, `github.repo`) and update the `github.pr.count` brief/note to open/draft only in `registry/signals.yaml`; verify `make check-schema` passes
- [ ] 1.2 Run `make generate`; verify the diff in `internal/semconv/metrics.go` adds `MetricGitHubPrCreated`, `MetricGitHubPrMerged`, `MetricGitHubPrClosed` and `dashboards/github-activity.json` gains their panels, with no other changes

## 2. Config and filters

- [ ] 2.1 Add `PRFilters.Authors`, `PRFilters.HeadBranchPrefix` and `Target.RenovateDashboard.Enabled` (default false) in `internal/config/config.go`; verify config tests for parsing and the false default
- [ ] 2.2 Extend `filter.PR` with `Author` and `HeadRef` and `PRFilter.Match` with author (case-insensitive) and branch-prefix rules; verify `internal/filter/pr_test.go` cases for author, bot author, branch prefix, label and combined filters

## 3. PR collector

- [ ] 3.1 Replace `prQuery` with one query taking `$states`, `$labels`, `$orderBy`, `$cursor`; add `updatedAt`, `headRefName`, `author { __typename login }` to the node with `[bot]` normalisation; remove `defaultMaxPRsPerRepo`/`MaxPRsPerRepo`
- [ ] 3.2 Implement the open scan (OPEN, no cap) feeding `github.pr.count` (open/draft), `github.pr.age`, `github.pr.review_status`; verify a test with more than 500 open PRs over several pages reports the exact count
- [ ] 3.3 Implement the event scan (UPDATED_AT desc, stop past window start, dedup by number, window `(start, end]`, record only after both scans succeed) and the three counters with first-cycle `Add(0)`; verify tests for stop-at-cutoff page count, lookback cap, created/merged/closed counts, filters on events, and no events on first cycle
- [ ] 3.4 Pass the cycle start from `Runner` into `PRCollector.Collect` as the window end; verify a runner test where two consecutive cycles over the same fixture record each event once, and the existing failed-repo test still passes
- [ ] 3.5 Update `pr_fixture.go` to paginate, honour order/states/labels variables and count requests

## 4. Dashboard collector opt-in and docs

- [ ] 4.1 Build the dashboard collector in `cmd/github-activity-exporter/main.go` only when `renovate_dashboard.enabled`; verify a test that a disabled target gets no dashboard collector and the fake issue client receives no request
- [ ] 4.2 Update README: config block (`authors`, `head_branch_prefix`, `renovate_dashboard`), Renovate preset, metrics table (three counters, `github.pr.count` open/draft only); verify markdown lint on README.md

## 5. Validation

- [ ] 5.1 Run `go build ./...`, `go test ./...`, `make check-schema`, `make generate` (no diff) and the OTLP and `/metrics` live-check integration tests; verify all pass

## Workflow follow-up

- Archive the change with `openspec archive pr-selection-and-rates` before the final commit of the PR.
- PR body: "Closes #26, closes #24", assignee gjed.
