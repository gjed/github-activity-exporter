# Tasks

## 1. Templates

- [ ] 1.1 Move the macros to `templates/grafana/_macros.j2` (explicit `ctx`/state, `pos.used` tracking, skeleton macro) and import them from `dashboard.json.j2`; verify `make generate` renders the KPI dashboard
- [ ] 1.2 Strip `dashboard.json.j2` to `github.*`/`renovate.*` panels, resolve `job`/`exporter.target` from the alternation of its queried series, title `GitHub Activity`, add the KPI scope check; verify by `make generate` and by a temporary self-metric panel failing generation
- [ ] 1.3 Add `exporter.json.j2` (variables, Collection/GitHub API/OTLP/Self-telemetry rows, `scrape.overruns` panel, coverage check) and list it in `weaver.yaml`; verify `make generate` writes `dashboards/github-activity-exporter.json` and a temporarily removed panel fails generation
- [ ] 1.4 Update the README dashboard and self-telemetry sections; verify with the markdown lint on `README.md`

## 2. Helm chart

- [ ] 2.1 Add the `dashboards/github-activity-exporter.json` symlink, restructure `dashboard.*` values, loop the dashboard template over `kpi`/`exporter` with the min_step patch and the old-key failure; verify `helm lint --strict` and `helm template | kubeconform -strict` with each dashboard on and off, min_step override, and old keys failing
- [ ] 2.2 Update `examples/github-app-values.yaml` and the chart README (values, rename note); verify the chart check loop over examples and the markdown lint
- [ ] 2.3 Widen the `.github/workflows/chart.yml` path filter to `dashboards/*.json`; verify with `actionlint` if available, otherwise by reading the diff

## 3. e2e

- [ ] 3.1 Add the telemetry toggle to `startExporter`, iterate `TestDashboardQueries` over self-telemetry on/off and every `dashboards/*.json`, with the self-telemetry empty-result rule and the KPI no-self-telemetry assertion; verify `make test-e2e` passes and paste the per-dashboard results for both runs
- [ ] 3.2 Iterate `TestDashboardContinuity` over every dashboard; verify in the same `make test-e2e` run

## 4. Integration

- [ ] 4.1 `go build ./...`, `go test ./...`, `make check-schema`, `make generate` with no diff beyond the intended dashboards; paste output
- [ ] 4.2 If [#91](https://github.com/gjed/github-activity-exporter/issues/91) has merged, rebase and give its secondary-limit metric a panel; verify `make generate` and the e2e

## Workflow follow-up

- `openspec archive dashboard-split` before the final commit
- Show the diff for approval, then push and open the PR (assignee gjed, body "Closes #90")
