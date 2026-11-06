# Tasks

## 1. Chart

- [x] 1.1 Scaffold `charts/github-activity-exporter` (Chart.yaml, values.yaml, `_helpers.tpl`, `.helmignore`) and the dashboard symlink; verify `helm lint` passes
- [x] 1.2 Add the ConfigMap, Deployment (Recreate, one replica, hardened, probes, checksums), and Secret templates with the validation errors; verify `helm template` renders defaults and fails for each conflicting combination in the spec
- [x] 1.3 Add the bridge Service, ServiceMonitor and dashboard ConfigMap templates; verify the rendered dashboard equals `dashboards/github-activity.json`
- [x] 1.4 Add `examples/renovate-values.yaml` (PAT) and `examples/github-app-values.yaml` (GitHub App, bridge, ServiceMonitor, dashboard); verify `helm lint --strict` and `helm template | kubeconform -strict` pass for defaults and both examples
- [x] 1.5 Write the chart README (config passthrough, secrets, probes vs `stall_timeout`/`collection_interval`, single replica, dashboard); verify markdown lint passes

## 2. CI and publishing

- [x] 2.1 Add the `check` job of `.github/workflows/chart.yml` on PRs touching the chart; verify its commands pass locally
- [x] 2.2 Add the `publish` job on release published / tag dispatch; verify `helm package --version 0.0.1 --app-version 0.0.1` produces a chart with the dashboard contents

## 3. Docs

- [ ] 3.1 Add the README "Kubernetes" section and the chart rows of the CI table; verify markdown lint passes

## 4. Integration

- [ ] 4.1 Install on a kind cluster in a namespace enforcing the `restricted` Pod Security Standard with the Renovate example and a `gh auth token` Secret; verify the pod is Ready, `/healthz` and `/readyz` answer 200, and the first cycle's logs; delete the cluster

## Workflow follow-up

- Archive the change with `openspec archive helm-chart` before the final commit of the PR.
