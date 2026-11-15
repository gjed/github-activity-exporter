# Tasks

## 1. Chart

- [x] 1.1 Add `dashboard.namespace`, `dashboard.kpi.namespace`, `dashboard.exporter.namespace` (default `""`, commented) to `values.yaml` and render `metadata.namespace` on both ConfigMaps in `templates/dashboard.yaml` with the per-dashboard → global → release namespace fallback; rename guard, `minStep` and `.Files.Get` checks untouched; verify `helm lint --strict` and `helm template` (defaults + both dashboards, `dashboard.namespace=observability`, plus `dashboard.exporter.namespace=platform`) piped to `kubeconform -strict`, showing `metadata.namespace` of both ConfigMaps
- [x] 1.2 Add the commented `namespace` value to `examples/github-app-values.yaml`; verify the chart CI loop (`helm lint --strict` + `helm template | kubeconform -strict` over defaults and every example) as `.github/workflows/chart.yml` runs it
- [x] 1.3 Document the values in the chart README "Grafana dashboards" section with the own-namespace sidecar example and the permission/ArgoCD destination note; verify the markdown lint on the README

## 2. Install test

- [x] 2.1 On a local kind cluster: install the release in namespace `gae` with both dashboards and `dashboard.namespace=monitoring`; show both ConfigMaps in `monitoring` and none in `gae`; `helm uninstall` and show them gone; delete the cluster

## Workflow follow-up

- `openspec archive chart-dashboard-namespace` before the final commit
- Show the diff for approval, then push and open the PR (assignee gjed, body "Closes #125")
