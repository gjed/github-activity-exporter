# Design

## Context

`templates/dashboard.yaml` loops over `kpi` and `exporter`, fails on the renamed `dashboard.enabled`/`labels`/`annotations` keys, reads each generated JSON with `.Files.Get`, patches `min_step` when `dashboard.minStep` is set, and renders one ConfigMap per enabled dashboard with no `metadata.namespace`. No chart template sets a namespace today. Each dashboard already has its own `labels` and `annotations`, so the two can target different sidecar selectors and folders.

## Goals / Non-Goals

**Goals:**

- Put the dashboard ConfigMaps in a namespace other than the release namespace with one value.
- Default rendering unchanged, so `helm template | kubectl apply -n <ns>` keeps working.

**Non-Goals:**

- Namespace overrides for any other chart resource (Deployment, Service, ServiceMonitor, Secret, ConfigMap of the config).
- Creating the target namespace or any RBAC for it.
- Changing the rename guard, the `minStep` patch or the `.Files.Get` checks.

## Decisions

### Namespace resolution in the template

Inside the existing loop:

```yaml
{{- with $values.namespace | default $.Values.dashboard.namespace }}
namespace: {{ . }}
{{- end }}
```

Empty strings fall through `default`, so a per-dashboard `""` means `dashboard.namespace`, and with both empty the field is omitted like on every other chart resource, so the object lands in the release namespace.

Alternative: always render the field, falling back to `$.Release.Namespace`. Rejected in review: `helm template` without `--namespace` then renders `namespace: default` on these two objects only, and `kubectl apply -n <ns>` rejects them, a workflow that works without the change. ArgoCD passes the destination namespace to `helm template` either way.

### Per-dashboard overrides included

`dashboard.kpi.namespace` and `dashboard.exporter.namespace` cost one more `default` in the same expression, no extra template branch, and fit the existing per-dashboard model: the two ConfigMaps already carry separate `labels` and `annotations` so they can match different sidecars, and a sidecar is selected by label and namespace together. Sending the KPI dashboard to a team Grafana and the exporter dashboard to a platform Grafana in another namespace needs both halves. `dashboard.namespace` stays the single knob for the common case.

Alternative: global `dashboard.namespace` only. Rejected: per-dashboard labels without per-dashboard namespace leaves the split half done, and the issue allows the override at the cost of a `default`.

### Values layout

`dashboard.namespace: ""` sits next to `dashboard.minStep` (applies to every dashboard); `namespace: ""` goes into `kpi` and `exporter` next to `labels`/`annotations`, each with a comment naming the fallback.

## Risks / Trade-offs

- [Installer lacks permission in the target namespace] → Helm or ArgoCD fails to create the ConfigMaps. Documented in the README: the Helm user or ArgoCD project needs create/update/delete on ConfigMaps there, and the ArgoCD Application's project `destinations` must allow that namespace.
- [Target namespace does not exist] → install fails. The chart does not create it; documented as a prerequisite.
- [Moving the namespace on upgrade] → Helm deletes the ConfigMap from the old namespace and creates it in the new one; the sidecar removes and re-adds the dashboard. No data loss: the dashboards are generated files.
