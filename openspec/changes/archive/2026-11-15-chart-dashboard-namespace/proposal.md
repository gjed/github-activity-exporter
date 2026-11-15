# Proposal

## Why

Both dashboard ConfigMaps always land in the release namespace, and the Grafana chart's dashboard sidecar only watches its own namespace unless `sidecar.dashboards.searchNamespace` is set. Today the exporter has to be installed in Grafana's namespace for its dashboards to load ([#125](https://github.com/gjed/github-activity-exporter/issues/125)).

## What Changes

- New value `dashboard.namespace` (default empty, meaning the release namespace) sets `metadata.namespace` of both dashboard ConfigMaps.
- New values `dashboard.kpi.namespace` and `dashboard.exporter.namespace` (default empty) override it per dashboard.
- `templates/dashboard.yaml` always renders `metadata.namespace`: the per-dashboard value, else `dashboard.namespace`, else the release namespace, so `helm template | kubectl apply` and ArgoCD place the ConfigMaps explicitly.
- Chart README "Grafana dashboards": the values, an example for a sidecar that only watches its own namespace, and the permissions the installer and the ArgoCD Application need in the target namespace.
- `examples/github-app-values.yaml` (enables both dashboards): commented `namespace` value.

The rename guard for `dashboard.enabled`/`labels`/`annotations`, the `minStep` handling and the `.Files.Get` checks stay unchanged. No other chart resource changes namespace.

## Capabilities

### New Capabilities

### Modified Capabilities

- `helm-chart`: the Grafana dashboard ConfigMap requirement places the ConfigMaps in a configurable namespace instead of always the release namespace.

## Impact

- `charts/github-activity-exporter/templates/dashboard.yaml`, `values.yaml`, `README.md`, `examples/github-app-values.yaml`.
- Default rendering gains an explicit `metadata.namespace: <release namespace>` on the dashboard ConfigMaps; installed objects are unchanged.
- Generated `dashboards/*.json` are not touched.
