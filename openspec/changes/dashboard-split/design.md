# Design

## Context

`templates/grafana/dashboard.json.j2` renders one dashboard from the v2 resolved registry (`weaver.yaml`, `application_mode: single`). Its macros (`check_metric`, `sel`, `by_clause`, `expr`, `brief`, `target`, `row`, `panel`, `variable`) read the global `ctx` and mutate a `pos` namespace for the grid. `$job` and `$target` resolve from `github_exporter.scrape.duration_count`. PR and issue collection run for every target; the Dependency Dashboard collector is opt-in per target. Self-telemetry metrics (`registry/telemetry.yaml`) exist only when `telemetry.enabled` and `telemetry.metrics.enabled`; the `github_exporter.*` metrics are always on. The chart embeds `dashboards/github-activity.json` through a symlink in `charts/github-activity-exporter/dashboards/`.

## Goals / Non-Goals

**Goals:**

- Two generated dashboards whose scope is enforced by `make generate`, not by review.
- KPI variables independent of every self-metric.
- One macro implementation shared by both templates.

**Non-Goals:**

- New panels for metrics not shown today, apart from `github_exporter.scrape.overruns` (required by "covers every `github_exporter.*` metric"). The self-telemetry row keeps the four #74 panels; `go.memory.allocations`, `go.processor.limit`, `go.config.gogc`, `go.schedule.duration` and `http.client.request.body.size` stay without a panel.
- Registry or Go changes. Chart namespace override (none exists today; ConfigMaps stay in the release namespace).

## Decisions

### Shared macros through an imported file

`templates/grafana/_macros.j2` holds every macro; both `dashboard.json.j2` (KPI) and `exporter.json.j2` import it (`{% import "_macros.j2" as m %}`). minijinja imports do not share the caller's globals, so `ctx` and a per-dashboard state namespace (`pos`: grid x/y/id plus the list of queried metric names) are passed explicitly. Verified with weaver 0.28: an imported macro mutates the caller's namespace, and a template not listed in `weaver.yaml` is not rendered on its own. `weaver.yaml` lists both templates with the same filter and `file_name`s `github-activity.json` and `github-activity-exporter.json`. The dashboard skeleton (header, `datasource` and `min_step` variables, footer) is a macro too, so the two templates only differ in panels, variables, title and uid.

Alternative: `{% extends %}` with blocks. Rejected: blocks still need the macros, so it adds a layer without removing the import.

### Scope enforced at generation

`expr` appends each metric name to `pos.used`. After the panels, the KPI template fails (undefined-function call, as the existing checks) on any used name outside `github.` and `renovate.`; the exporter template fails on any `github_exporter.*` metric of `ctx` missing from `pos.used`. A new `github_exporter.*` metric (for example the secondary-limit metric of [#91](https://github.com/gjed/github-activity-exporter/issues/91)) then breaks `make generate` until it gets a panel.

### KPI variables from the KPI's own series

`$job` and `$target` use `label_values({__name__=~"<names>", job=~"(.+/)?github-activity-exporter"}, job)` and `label_values({__name__=~"<names>", job=~"$job"}, exporter.target)`, where `<names>` is the escaped alternation of every series the KPI dashboard queries, built from `pos.used` (counters and gauges by name, histograms by `<name>_count`). A target resolves as long as any KPI panel has data for it: with the Dependency Dashboard collector off it resolves from the PR and issue series, and a self-metric can never make it appear.

Alternatives: `github.pr.count` alone (simplest, but a target whose PR filters leave no open PR series would vanish); a static `(github|renovate)\..+` regex (not registry-driven; any future `github.*` self-metric would leak in).

`$org` and `$repo` keep their selectors (`github.org`/`github.repo` exist only on product metrics).

### Exporter dashboard

Variables: `datasource`, `job` and `exporter.target` from `github_exporter.scrape.duration_count` (always on), `min_step`. No `org`/`repo`: no exporter metric declares them. Rows: Collection (cycle duration p50/p90, overruns per hour), GitHub API (errors per hour, rate limit remaining, time until reset), OTLP (push errors, `per_target=false` as today), Self-telemetry (the four #74 panels, `per_target=false`).

Identity: the KPI dashboard keeps uid `github-activity-exporter` so existing bookmarks and provisioned copies update in place; its title becomes `GitHub Activity`. The exporter dashboard gets uid `github-activity-exporter-internals`, title `GitHub Activity Exporter`. Giving the old uid to the exporter dashboard would silently replace users' KPI dashboard with the exporter one.

### Chart values

```yaml
dashboard:
  minStep: ""          # applies to every shipped dashboard
  kpi:
    enabled: false
    labels: { grafana_dashboard: "1" }
    annotations: {}
  exporter:
    enabled: false
    labels: { grafana_dashboard: "1" }
    annotations: {}
```

One template, `templates/dashboard.yaml`, loops over `kpi` and `exporter` with the existing min_step patch. KPI keeps ConfigMap `<fullname>-dashboard` and key `github-activity.json`, so an upgrade updates the existing object; the exporter one is `<fullname>-exporter-dashboard`. The old `dashboard.enabled`/`labels`/`annotations` keys fail rendering with the replacement key name: a silent ignore would drop users' dashboard on upgrade, and a compatibility alias keeps two spellings forever. The chart is 0.x; the rename is documented in the chart README.

Alternative: alias old keys to `kpi`. Rejected for the reason above.

### e2e

`TestDashboardQueries` gets two subtests, `self-telemetry on` and `off`, each with its own Prometheus and exporter (`startExporter` takes the telemetry toggle). Each loads every `dashboards/*.json`, resolves its variables and runs every target. A target is a self-telemetry target when its expression names a metric from `registry/telemetry.yaml`: with telemetry off it must be empty, every other target must have data. The KPI dashboard must have no self-telemetry target. The "registry metrics are in Prometheus" check expects the telemetry series only when on. `TestDashboardContinuity` (telemetry on) checks the `rate()` panels of every dashboard. Failure messages name the dashboard file.

## Risks / Trade-offs

- [e2e runs the exporter twice] → about one extra warm-up per run; the 600s `make test-e2e` timeout still holds (measured during implementation).
- \[Long `__name__` alternation in the KPI variables\] → bounded by the KPI metric count (13 names today); Prometheus handles it in `match[]`.
- [Chart values rename breaks existing values files] → fail-fast with the new key name, README migration note.
- [#91 lands first] → rebase; the generation check forces its metric onto the exporter dashboard.

## Migration Plan

Chart users replace `dashboard.enabled/labels/annotations` with `dashboard.kpi.*` and opt into `dashboard.exporter.enabled`. Dashboard importers keep the KPI uid and import the exporter dashboard separately. Rollback: previous chart version.
