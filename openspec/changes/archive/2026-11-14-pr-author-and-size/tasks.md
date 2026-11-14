# Tasks

## 1. Registry and generation

- [x] 1.1 Add `github.pr.author` (string, `opt_in` on the histograms) to `registry/attributes.yaml`, and `github.pr.lines.added` / `github.pr.lines.removed` (histogram, `{line}`, `annotations.prometheus`) to `registry/signals.yaml`; add the attribute to `github.pr.close.duration`; verify `make check-schema` passes
- [x] 1.2 Run `make generate` and verify the new constants in `internal/semconv` and no other diff

## 2. Config

- [x] 2.1 Add top-level `collection.pr_author` (default false) and per-target `collection.pr_author` override in `internal/config/config.go`, resolved by `Load`; verify config tests for default, global, override, env override and invalid value

## 3. Collector

- [x] 3.1 Read `additions` and `deletions` in the PR query; record the size histograms (explicit boundaries from design.md) and the optional author on the three histograms from the existing merged/closed event branch; wire the setting through `PRCollectorConfig` in `main.go`
- [x] 3.2 Unit tests: each PR recorded once across consecutive cycles and with concurrency > 1 under `go test -race`; author only when enabled; bot login verbatim; deleted author without the attribute; existing PR metrics unchanged with the setting off

## 4. Dashboard

- [x] 4.1 Add the PR size panels (p50/p90 per repo) and the per-author panel to `templates/grafana/dashboard.json.j2`; run `make generate` and verify the KPI dashboard and chart copies change only by those panels
- [x] 4.2 Turn `collection.pr_author` on in the e2e config and give the mock GitHub PRs authors and sizes; verify the dashboard e2e passes over every generated dashboard

## 5. Live-checks

- [x] 5.1 Run the OTLP and `/metrics` live-checks with the setting on and off; verify they pass

## 6. Docs and chart

- [x] 6.1 README: the setting, the series cost, and the bot login difference against the `authors` filter; verify markdown lint passes
- [x] 6.2 Chart: `examples/pr-author-values.yaml` and a cardinality warning in the chart README; verify markdown lint and `helm template` with the example

## 7. Validation

- [x] 7.1 `go build ./... && go test -race ./... && make check-schema && make generate` with no unexpected diff
- [x] 7.2 Real-API run against zextras (read-only): GraphQL cost per cycle before and after, and series count with the setting on and off

## Workflow follow-up

- Archive the change with `openspec archive pr-author-and-size` before the final commit of the PR.
