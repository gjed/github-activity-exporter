# Proposal

## Why

Nobody can tell from the project which metric names a backend stores. The registry says `github.pr.count`, the Prometheus bridge emits `github.pr.automerged_total` and `github_exporter.scrape.duration_seconds` (`NoUTF8EscapingWithSuffixes`), and a backend with default OTLP translation stores `github_pr_count`. Nothing declares the naming policy and nothing checks it ([#14](https://github.com/gjed/github-activity-exporter/issues/14)). This change makes the registry name the stored name on every path and enforces that in CI.

## What Changes

- Naming contract: the registry metric name is the exposed name, byte for byte (`github.pr.count` stays `github.pr.count`): no `_total`, no unit suffix, no underscore escaping. The only derived names are the classic histogram sample series `<name>_bucket`, `<name>_sum`, `<name>_count`, which the Prometheus exposition format requires. The contract lives in `registry/manifest.yaml` (`description` plus a header comment) and in the `weaver-schema` spec.
- Backend requirements, documented in the manifest, spec and README:
  - Prometheus 3.x: UTF-8 metric names (the 3.x default `metric_name_validation_scheme: utf8`), and for OTLP ingestion `--web.enable-otlp-receiver` with `otlp.translation_strategy: NoTranslation`.
  - Mimir: `-validation.name-validation-scheme=utf8` and `-distributor.otel-translation-strategy=NoTranslation` (per-tenant `name_validation_scheme: utf8`, `otel_translation_strategy: NoTranslation`), with `-distributor.otel-metric-suffixes-enabled=false` (the default). Grafana Cloud Metrics runs Mimir and needs the same per-tenant limits on the stack.
- Prometheus bridge (`internal/exporter/provider.go`): `otlptranslator.NoUTF8EscapingWithSuffixes` -> `otlptranslator.NoTranslation`. Operator-visible: on `/metrics`, `github.pr.age_seconds` -> `github.pr.age`, `github.pr.automerged_total` -> `github.pr.automerged`, `github_exporter.scrape.duration_seconds_bucket` -> `github_exporter.scrape.duration_bucket`, and so on.
- Generated code: `templates/go/metrics.go.j2` also emits a list of every registry metric with its instrument type and unit, so tests can enumerate the registry without hardcoding names.
- CI checks:
  - `/metrics`: a unit test records every registry metric through the bridge, scrapes `/metrics`, and fails unless the set of exposed metric families equals the set of registry names and every sample name is the family name (plus `_bucket`/`_sum`/`_count` for histograms).
  - OTLP: a new live-check integration test emits every registry metric, reads the live-check JSON report, and fails unless the metric names live-check saw are exactly the registry names.
- README: replace "names are translated to Prometheus naming conventions automatically" with the contract and the backend requirements.

Not marked **BREAKING** (project stays on 0.x). Operators scraping `/metrics` see new series names; OTLP output is unchanged.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `weaver-schema`: the registry declares the naming contract and the backend requirements; the live-check test asserts emitted OTLP names equal the registry names.
- `metric-export`: the Prometheus bridge exposes registry names verbatim (NoTranslation), checked by a test against the registry.

## Impact

- Code: `internal/exporter/provider.go`, new test in `internal/exporter/`, `tests/integration/live_check_test.go`.
- Registry/templates: `registry/manifest.yaml`, `templates/go/metrics.go.j2`; regenerated `internal/semconv/metrics.go`. Dashboard JSON unchanged.
- Docs: `README.md`, `openspec/specs/weaver-schema`, `openspec/specs/metric-export`.
- Out of scope: dashboard query changes ([#19](https://github.com/gjed/github-activity-exporter/issues/19)).
