# Tasks

## 1. Dashboard template

- [x] 1.1 Add the type-keyed query macros and the metric lookup to `templates/grafana/dashboard.json.j2`; a missing metric or wrong type fails `make generate` (verify with a scratch edit, not committed)
- [x] 1.2 Declare the variables `datasource`, `job`, `target`, `org`, `repo`, chained, resolving `job` against `github-activity-exporter`
- [x] 1.3 Declare the panels: Pull requests, Issues, GitHub API, Exporter self-metrics, Dependency Dashboard rows
- [x] 1.4 `templates/grafana/weaver.yaml` unchanged (the filter keeps `annotations`/`attributes`); run `make generate` and commit the regenerated `dashboards/github-activity.json`
- [x] 1.5 Import into a local Grafana (13.3.0) with a UTF-8 Prometheus datasource; variables resolve and legends render the label values

## 2. e2e

- [x] 2.1 Mock GitHub API in `tests/e2e` (PAT check, repo listing, GraphQL PRs with fresh events per cycle, issues with a Dependency Dashboard issue, rate limit headers, one failed issue listing)
- [x] 2.2 Prometheus 3.x container with the OTLP receiver and `NoTranslation`; OTLP proxy rejecting the first push
- [x] 2.3 Build and run the exporter binary against both; wait for enough samples
- [x] 2.4 Query runner: resolve variables, substitute, run every panel target, fail on error/empty/all-NaN with panel, refId, query and response
- [x] 2.5 `make test-e2e`; `Dashboard E2E` job in `.github/workflows/ci.yml` with the pinned image

## 3. Docs and validation

- [x] 3.1 `README.md` dashboard section; markdown lint on changed `.md` files
- [x] 3.2 `go build ./...`, `go test ./...`, `make check-schema`, `make generate` with no diff, `make test-e2e` passing
- [x] 3.3 `openspec archive dashboard-from-contract` before the final commit
