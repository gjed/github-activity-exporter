# Design

## Context

Every dashboard query comes from the `expr` macro in `templates/grafana/_macros.j2`. Its gauge branch renders:

```promql
<shape> by (<panel labels>) (last_over_time(<selector>[$__rate_interval]))<offset>
```

`<shape>` is `sum`, `max` or `min`. The selector filters by `job`, `exporter.target` (unless `per_target=false`), `github.org`/`github.repo` when declared, and panel matchers. Nothing removes the labels that tell exporter processes apart, so the outer aggregation spans one series per process. Under the OTLP receiver those are `instance` (from `service.instance.id`, a new UUID per process) and any promoted resource attribute (`k8s.pod.name`, `host.name`). `job` comes from `service.namespace`/`service.name` and is the same before and after a restart. See proposal.md for the impact.

## Goals / Non-Goals

**Goals:**

- One value per metric identity on every gauge panel, whatever the number of processes that wrote it inside the window.
- The fix lives in the macro: present and future gauge panels on both dashboards get it, and a metric that gains an attribute is covered without a template change.

**Non-Goals:**

- `rate()` and `histogram_quantile()` panels. A counter's series restarts at zero in the new process and `rate()` handles each series on its own; summing rates of old and new pod is correct.
- Panel layout, legends, variables or any other macro.
- Making `service.instance.id` stable across restarts.

## Decisions

### D1. Inner `max by (<identity>)`, built from the registry

The gauge branch renders:

```promql
<shape> by (<panel labels>) (max by (job[, "exporter.target"], <metric attributes>) (last_over_time(<selector>[$__rate_interval])))<offset>
```

Identity is `job`, then `exporter.target` when `per_target` is true and the metric does not already declare it, then every key of `m.attributes` in registry order. For `github.pr.count`:

```promql
sum by ("github.pr.state") (max by (job, "exporter.target", "github.org", "github.repo", "github.pr.state", "github.pr.label", ...) (last_over_time({"github.pr.count", ...}[$__rate_interval])))
```

For `github_exporter.api.rate_limit.reset` the offset stays outside both aggregations: `max by (...) (max by (...) (last_over_time(...))) - time()`.

The inner clause is rendered directly, not through `by_clause`: `job` is not a registry attribute, and every other label in it comes from `m.attributes`, so the `dashboard_label_not_on_metric` check has nothing to catch there. The outer `by_clause` and the selector keep their generation-time checks unchanged.

Alternatives:

- Hard-coded label lists per panel (the issue's sketch): breaks silently when a metric gains an attribute, for example `github.pr.author` from [#132](https://github.com/gjed/github-activity-exporter/issues/132).
- `without (instance, "service.instance.id", "k8s.pod.name", "host.name")`: a deny list of whatever resource attributes a backend promotes. Any other promoted attribute (`k8s.node.name`, `container.id`, a custom `OTEL_RESOURCE_ATTRIBUTES` key) brings the double count back. An allow list from the registry does not depend on the backend's promotion settings.

### D2. `max` as the per-identity reducer

While old and new process both have a sample in the window, the identity has two values. Options:

- `max`: exact when both report the same value, which is the case in [#133](https://github.com/gjed/github-activity-exporter/issues/133): the count is continuous across the restart (about 1041 on both pods). When the value changed between the old pod's last push and the new pod's first cycle, the panel can show the old pod's higher value until the old sample leaves the window, at most one `$__rate_interval` after the old pod's last push. `last_over_time` already accepts that much staleness for a single process, so the dashboard gets no new kind of error, only the same bounded lag.
- `min`: symmetric to `max`, wrong in the other direction. No reason to prefer it.
- `avg`: during the overlap it is neither pod's value, a number no process ever reported.
- Newest sample wins (`topk by (identity) (1, timestamp(...))` joined back): would be exact, but PromQL has no direct "newest series" selector; the join costs a second evaluation of every gauge query and is fragile under staleness. Not worth it for a lag bounded by one window.

For the `min`-shaped Rate limit remaining panel, the inner `max` keeps the higher of the two pods' remaining calls during the overlap. Both pods draw on the same token's quota, so the values are close; the lag is the same bounded one.

### D3. Identity of the `per_target=false` self-telemetry panels

Go memory and Goroutines query `go.*` metrics with `per_target=false`. Their series carry no `exporter.target` (they are process-wide) and the registry declares only `go.memory.type` on `go.memory.used`. Identity is `job` plus the declared attributes:

- `exporter.target` is not added: the series do not carry it, and a process is not per target.
- `service.instance.id` / `instance` are not added: they are exactly the labels that change on a restart. Keeping them reproduces the bug.

Each exporter process is one pod, so per `job` the panel shows the current pod's value, or during the overlap the higher of old and new pod.

### D4. Several exporter Deployments sharing one `job`

`job` is `service.name` (default `github-activity-exporter`), optionally prefixed with `service.namespace/`. Two Deployments with the default resource share it.

- Product and `github_exporter.*` gauges: identity includes `exporter.target`, `github.org` and `github.repo` where declared. Deployments with different targets stay separate and the panel's outer `sum` adds them as before. Deployments that collect the same target name and repos are counted once instead of twice; that is the same double count as a restart.
- Self-telemetry gauges: no target in the identity, so Deployments sharing a `job` show the largest process instead of the sum of all processes. That is a change for this setup. Giving each Deployment its own `service.name` (`OTEL_SERVICE_NAME`) or `service.namespace` gives it its own `job`, which the `job` variable already selects. `docs/dashboards.md` states this.

## Risks / Trade-offs

- \[Panel shows the old pod's higher value for up to one `$__rate_interval` after a restart, when the value changed across it\] → Bounded by the window that `last_over_time` already uses; see D2.
- \[Self-telemetry panels no longer sum several Deployments sharing a `job`\] → Documented, with the per-Deployment `service.name` remedy; see D4.
- [A metric attribute that is absent on some series (empty label)] → `max by` treats an absent label as empty, the same as the selector does; no change.
- [Longer queries] → One extra aggregation per gauge query, over the same series; no extra series selection.

## Migration Plan

Regenerate the dashboards with `make generate`; the chart picks them up at the next release. Grafana users who imported the JSON re-import it. Rollback is the previous dashboard JSON.
