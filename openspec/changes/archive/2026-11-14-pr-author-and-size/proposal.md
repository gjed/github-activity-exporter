# Proposal: PR author and lines added/removed

## Why

The PR metrics count PRs and time them to close, but say nothing about how big a PR is or who opened it. Line counts per author answer "who ships large or small PRs" and "are bot PRs small and fast" ([#53](https://github.com/gjed/github-activity-exporter/issues/53)).

## What Changes

- New histograms `github.pr.lines.added` and `github.pr.lines.removed`, unit `{line}`, recorded once per selected PR merged or closed in the event window, from the existing event scan, next to `github.pr.close.duration`. No new scan and no extra request per PR.
- The PR GraphQL query also reads `additions` and `deletions`. `author { login }` is already in the query (the `authors` filter uses it).
- New attribute `github.pr.author`, the PR author login exactly as GraphQL returns it, on the two new histograms and on `github.pr.close.duration`. It is opt-in: setting `collection.pr_author`, top-level default `false`, overridable per target. When off, the histograms carry only `exporter.target`, `github.org` and `github.repo`, and `github.pr.close.duration` is unchanged.
- Registry: the two metrics and the attribute (requirement level `opt_in`), with `annotations.prometheus`; regenerated constants and dashboards.
- KPI dashboard: PR size panels (lines added and removed, p50/p90 per repo) and a per-author breakdown that is empty when the setting is off. The e2e config turns the setting on.
- Helm chart: a values example that enables the setting, and a cardinality warning in the chart README.
- README: the setting, its series cost, and how the recorded bot login (`renovate`) differs from the REST login (`renovate[bot]`) the `authors` filter uses.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `pr-collection`: PR size histograms, the `collection.pr_author` setting, and the `github.pr.author` attribute on the size histograms and `github.pr.close.duration`.
- `weaver-schema`: the KPI dashboard gains the PR size panels and the per-author panel.

## Impact

- Code: `internal/collector/pr.go`, `internal/config/config.go`, `cmd/github-activity-exporter/main.go` (wiring).
- Registry: `registry/attributes.yaml`, `registry/signals.yaml`; template `templates/grafana/dashboard.json.j2`; generated `internal/semconv/*.go`, `dashboards/*.json`, chart dashboard copies.
- Tests: collector unit tests, config tests, OTLP and `/metrics` live-checks with the setting on and off, dashboard e2e.
- Docs: `README.md`, `charts/github-activity-exporter/README.md`, a new chart example.
- GraphQL cost: unchanged (measured, see design.md). Series: see design.md.
- Overlap with [#76](https://github.com/gjed/github-activity-exporter/issues/76) (telemetry-logs) in `internal/collector/pr.go` and `README.md`; rebase if it merges first.
