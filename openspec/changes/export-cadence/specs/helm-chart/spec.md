## MODIFIED Requirements

### Requirement: Grafana dashboard ConfigMap

When `dashboard.enabled` is true the chart SHALL create a ConfigMap labelled `grafana_dashboard: "1"` (overridable) whose content is the repository's generated `dashboards/github-activity.json`, byte for byte unless `dashboard.minStep` is set. A non-empty `dashboard.minStep` SHALL replace the default of the dashboard's `min_step` variable and change nothing else.

#### Scenario: Dashboard matches the generated file

- **WHEN** `dashboard.enabled` is true and `dashboard.minStep` is empty
- **THEN** the ConfigMap data `github-activity.json` equals `dashboards/github-activity.json` at the chart's version

#### Scenario: Min step override

- **WHEN** `dashboard.enabled` is true and `dashboard.minStep` is `5m`
- **THEN** the dashboard in the ConfigMap has `min_step` defaulting to `5m` and is otherwise the same JSON document as `dashboards/github-activity.json`
