## Why

The `weaver-schema` spec says the registry declares every metric with all its attributes and their requirement levels. On `main` no metric declares attributes, `github_exporter.api.errors` lacks the `endpoint` attribute the `metric-export` spec requires, the resource attributes are undeclared, and live-check only catches type mismatches. Weaver 0.28 also warns on every signal group without `requirement_level` and has deprecated `weaver registry resolve` and `--include-unreferenced`. Issue #25.

## What Changes

- Every metric in `registry/signals.yaml` declares `requirement_level` and an `attributes:` block whose refs and requirement levels match what `internal/collector` and `internal/exporter` attach.
- New attribute `github.api.endpoint` (enum: `graphql`, `rest.issues`, `rest.repos`), declared on `github_exporter.api.errors` and recorded by `SelfMetrics.RecordAPIError`.
- New public attribute group `renovate_github_exporter.resource` declaring the resource attributes set by `buildResource` in `internal/exporter/provider.go` (`service.name` required; host, os, process and container attributes from the SDK detectors).
- `registry/manifest.yaml` keeps its name (weaver 0.28 default) and gains `file_format: definition_manifest/2.0`. The `weaver-schema` spec wording changes from `registry_manifest.yaml` to `manifest.yaml`.
- `.github/workflows/registry-validate.yml`: `weaver registry resolve` replaced by `weaver registry package --v2`.
- Live-check runs in v2 mode with a `.weaver.toml` resource matcher and without the deprecated `--include-unreferenced`. Undeclared metrics, attributes not declared on the emitting metric, undeclared resource attributes and missing required attributes are violations. A negative integration test proves the attribute case.
- Integration test weaver pin bumped from 0.23.0 to 0.28.0 to match CI and the Makefile.

Out of scope: metric naming/suffix policy (#14), rate-limit gauges (#16).

## Capabilities

### New Capabilities

### Modified Capabilities

- `weaver-schema`: manifest filename, attribute and requirement-level declarations per metric, signal requirement levels, declared resource attributes, live-check fails on undeclared metrics/attributes.
- `metric-export`: `github_exporter.api.errors` attribute keys are `exporter.target` and `github.api.endpoint`.

## Impact

- `registry/attributes.yaml`, `registry/signals.yaml`, `registry/manifest.yaml`
- `internal/exporter/selfmetrics.go`, `internal/collector/runner.go` (+ tests): `RecordAPIError` gains an `endpoint` argument
- `internal/semconv/attributes.go` (generated): adds `github.api.endpoint` constants
- `templates/go/weaver.yaml`, `templates/go/attributes.go.j2`: skip the resource group, casing replaces for the new enum
- `.weaver.toml` (new), `tests/integration/live_check_test.go`
- `.github/workflows/registry-validate.yml`
- Dashboard JSON: unchanged
