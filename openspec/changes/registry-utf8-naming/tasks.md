# Tasks

## 1. Registry contract and generated metric list

- [ ] 1.1 Add the naming contract and backend requirements to `registry/manifest.yaml` (`description` and header comment); verify `make check-schema` exits 0
- [ ] 1.2 Extend `templates/go/metrics.go.j2` to emit a generated list of every registry metric with its instrument type and unit; run `make generate` and verify the only diff is the new list in `internal/semconv/metrics.go` and `go build ./...` passes

## 2. Prometheus bridge

- [ ] 2.1 Switch `internal/exporter/provider.go` to `otlptranslator.NoTranslation` and update its comment; verify `go build ./...`
- [ ] 2.2 Add a unit test in `internal/exporter` that records every registry metric through the bridge, scrapes `/metrics`, and asserts family names equal registry names and sample names follow the histogram rule; verify `go test ./internal/exporter/...` passes, and fails when the strategy is reverted to `NoUTF8EscapingWithSuffixes`
- [ ] 2.3 Update the README Metrics section with the contract, the histogram sample rule, the OpenMetrics counter limitation and the Prometheus/Mimir/Grafana Cloud requirements; verify markdown lint passes on `README.md`

## 3. OTLP name check

- [ ] 3.1 Run live-check with `--format json` and add a live-check test that emits every registry metric and asserts the report's `seen_registry_metrics` covers every registry name and `seen_non_registry_metrics` is empty; verify `go test -tags integration -count=1 ./tests/integration/...` passes, and fails when a registry metric is dropped or a suffixed name is emitted

## 4. Integration

- [ ] 4.1 Run `go build ./... && go test ./... && make check-schema && make generate` and verify `git status` shows no unexpected diff
- [ ] 4.2 Archive the change with `openspec archive registry-utf8-naming` and verify `openspec/specs/weaver-schema` and `openspec/specs/metric-export` contain the new requirements
