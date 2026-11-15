## MODIFIED Requirements

### Requirement: Grafana dashboard ConfigMap

The chart SHALL ship each generated dashboard in its own ConfigMap, toggled separately:

- `dashboard.kpi.enabled` creates `<fullname>-dashboard` with data key `github-activity.json`, the repository's `dashboards/github-activity.json`.
- `dashboard.exporter.enabled` creates `<fullname>-exporter-dashboard` with data key `github-activity-exporter.json`, the repository's `dashboards/github-activity-exporter.json`.

Both default to disabled. Each ConfigMap SHALL carry its own `dashboard.<name>.labels` (default `grafana_dashboard: "1"`) and `dashboard.<name>.annotations`. Each ConfigMap SHALL always render `metadata.namespace`: `dashboard.<name>.namespace` when non-empty, else `dashboard.namespace` when non-empty, else the release namespace. The content SHALL be the generated file byte for byte unless `dashboard.minStep` is set; a non-empty `dashboard.minStep` SHALL replace the default of the `min_step` variable of every shipped dashboard and change nothing else. Rendering SHALL fail with a message naming the replacement key when `dashboard.enabled`, `dashboard.labels` or `dashboard.annotations` is set.

#### Scenario: Dashboard matches the generated file

- **WHEN** `dashboard.kpi.enabled` and `dashboard.exporter.enabled` are true and `dashboard.minStep` is empty
- **THEN** each ConfigMap's data equals its generated `dashboards/*.json` file at the chart's version

#### Scenario: Each dashboard toggles alone

- **WHEN** only one of `dashboard.kpi.enabled` and `dashboard.exporter.enabled` is true
- **THEN** only that dashboard's ConfigMap is rendered

#### Scenario: Separate labels and folder annotations

- **WHEN** `dashboard.kpi.annotations` and `dashboard.exporter.annotations` set different `grafana_folder` values and the labels differ
- **THEN** each ConfigMap carries only its own labels and annotations

#### Scenario: Min step override

- **WHEN** a dashboard is enabled and `dashboard.minStep` is `30m`
- **THEN** that dashboard in its ConfigMap has `min_step` defaulting to `30m` and is otherwise the same JSON document as its generated file

#### Scenario: Old dashboard keys

- **WHEN** `dashboard.enabled`, `dashboard.labels` or `dashboard.annotations` is set
- **THEN** rendering fails and the message names `dashboard.kpi.enabled`, `dashboard.kpi.labels` or `dashboard.kpi.annotations`

#### Scenario: Release namespace by default

- **WHEN** both dashboards are enabled and no `namespace` value is set
- **THEN** both ConfigMaps render `metadata.namespace` equal to the release namespace

#### Scenario: Dashboard namespace

- **WHEN** both dashboards are enabled and `dashboard.namespace` is `observability`
- **THEN** both ConfigMaps render `metadata.namespace: observability` and every other chart resource stays in the release namespace

#### Scenario: Per-dashboard namespace

- **WHEN** both dashboards are enabled, `dashboard.namespace` is `observability` and `dashboard.exporter.namespace` is `platform`
- **THEN** the KPI ConfigMap renders `metadata.namespace: observability` and the exporter ConfigMap renders `metadata.namespace: platform`
