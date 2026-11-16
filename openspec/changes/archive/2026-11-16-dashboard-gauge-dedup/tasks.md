# Tasks

## 1. Regression test

- [x] 1.1 Add `TestDashboardGaugesAcrossInstances` to `tests/e2e/dashboard_e2e_test.go`: one Prometheus, one mock GitHub, two exporter processes (self-telemetry on, distinct random `service.instance.id`, so distinct `instance`) pushing OTLP straight to Prometheus. After both have samples, for every gauge target (`last_over_time(`) of every generated dashboard, evaluate the panel at one fixed time with the dashboard's variables and scaled `$__rate_interval`, then the same query restricted to each `instance`; per legend series, the panel value must equal the maximum over the two restricted values. Verify it fails on the `origin/main` dashboards (`git checkout origin/main -- dashboards/` in the worktree, `go test -tags e2e -run TestDashboardGaugesAcrossInstances ./tests/e2e/`, then `git checkout HEAD -- dashboards/`) with Open PRs by state at twice the single-instance value.

## 2. Gauge identity in the macro

- [x] 2.1 Change only the gauge branch of `expr` in `templates/grafana/_macros.j2` to wrap `last_over_time(...)` in `max by (job[, "exporter.target"], <m.attributes keys>)`, `exporter.target` only when `per_target` and not already declared; update the `expr` comment and the header comment's gauge line. Verify `make generate` succeeds and `git status` shows only `dashboards/github-activity.json` and `dashboards/github-activity-exporter.json` changed, with every `last_over_time` query wrapped and no `rate(`/`histogram_quantile(` query changed (`git diff -U0 dashboards/`).
- [x] 2.2 Verify the Time until rate limit reset query renders as `max by (...) (max by (...) (last_over_time(...))) - time()` and still returns data in `make test-e2e`.
- [x] 2.3 Verify the generation-time checks still fire: temporarily add a gauge panel with an undeclared `by` label to a template, `make generate` fails with `dashboard_label_not_on_metric`; revert.
- [x] 2.4 Update `docs/dashboards.md`: the gauge query shape (`last_over_time` reduced with `max by` the metric identity, so a restart does not double gauges) and the note that Deployments sharing a `job` show the largest process on the self-telemetry gauges; lint with `pre-commit run --config ~/.config/pre-commit/.pre-commit-config.yaml --files docs/dashboards.md`.
- [x] 2.5 Verify `TestDashboardGaugesAcrossInstances` passes on the branch.

## 3. Validation

- [x] 3.1 `go build ./...`, `go test -race ./...`, `golangci-lint run`, `make check-schema` pass.
- [x] 3.2 `make generate` leaves no diff beyond the committed dashboards; `make test-e2e` passes, including `TestDashboardQueries` with self-telemetry on and off.

## Workflow follow-up

- Run `openspec archive dashboard-gauge-dedup` before the final commit, in the same PR.
- PR body: `Closes #133`; assignee gjed. No push before the diff is approved.
