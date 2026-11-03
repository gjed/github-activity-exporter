# Design

## Context

See proposal.md for motivation. Current state:

- OTLP carries the raw instrument name, which is already the registry name. Live-check already fails on a metric name the registry does not declare.
- The bridge uses `otlptranslator.NoUTF8EscapingWithSuffixes` (otlptranslator v1.0.0, OTel Prometheus exporter v0.67.0) and serves `/metrics` with `EnableOpenMetrics: true`.
- `internal/semconv/metrics.go` holds one string constant per registry metric and nothing that lists them.

Real bridge output for the same four instruments (one data point each), recorded with a throwaway test against the pinned dependencies:

| Registry name / instrument                            | `NoUTF8EscapingWithSuffixes` (today)                         | `NoTranslation`                                                            |
| ----------------------------------------------------- | ------------------------------------------------------------ | -------------------------------------------------------------------------- |
| `github.pr.count` updowncounter                       | `github.pr.count` gauge                                      | `github.pr.count` gauge                                                    |
| `github.pr.age` gauge, unit `s`                       | `github.pr.age_seconds` gauge                                | `github.pr.age` gauge                                                      |
| `github.pr.automerged` counter                        | `github.pr.automerged_total` counter                         | `github.pr.automerged` counter (text format), `unknown` (OpenMetrics text) |
| `github_exporter.scrape.duration` histogram, unit `s` | `..._seconds_bucket`, `..._seconds_sum`, `..._seconds_count` | `github_exporter.scrape.duration_bucket`, `_sum`, `_count`                 |

Histograms: `NoTranslation` drops the unit suffix but keeps `_bucket`/`_sum`/`_count`. They are part of the classic histogram exposition format, not translation, so no strategy removes them. The family (`# TYPE`) name is the registry name.

## Goals / Non-Goals

**Goals:**

- One rule maps the registry to every exposed name, and CI fails when either output breaks it.

**Non-Goals:**

- Native histograms or NHCB conversion.
- Configuring any backend; we only document what it must be set to.
- Dashboard queries ([#19](https://github.com/gjed/github-activity-exporter/issues/19)).

## Decisions

### NoTranslation over NoUTF8EscapingWithSuffixes

Suffixes make the stored name depend on unit and instrument type, so consumers need a translation table. With `NoTranslation` the stored name is the registry name, and only histogram sample series are derived, by a fixed rule. Alternative (keep suffixes, declare the rule) rejected by the issue decision.

### Contract in the manifest `description` plus a header comment, not per-metric annotations

Weaver 0.28 silently drops unknown manifest keys (an extra `naming:` block passes `registry check` and is absent from `registry package` output), so it cannot hold a machine-readable flag. Per-metric `annotations` survive resolution, but under `NoTranslation` each would repeat the metric name: no information. The rule (identity, plus histogram sample suffixes) is stated once in the manifest and the spec, and the tests enforce it.

### Generated metric list feeds both checks

`metrics.go.j2` gains a generated slice of `{Name, Instrument, Unit}` for every registry metric. The tests iterate it to create one instrument per metric with the registry's kind and unit; the unit matters because suffixing strategies append `_seconds` to gauges and histograms with unit `s`. Alternative: run `weaver registry package` inside the tests and parse `resolved.yaml`. Rejected: it makes the unit test depend on Docker/weaver, and the slice is derived from the same registry. A registry metric missing from the generated slice is impossible because `make generate` drift fails `Check Generated Files`.

### Where each check runs

- `/metrics` check: unit test in `internal/exporter`. It needs no weaver, so it runs in the `Test` job on every push and PR.
- OTLP check: a new live-check integration test, `TestLiveCheck_NamesEqualRegistry`, reusing the existing receiver harness. Live-check runs with `--format json`; its final statistics document lists `seen_registry_metrics` and `seen_non_registry_metrics`. The test fails unless every registry metric was seen and no non-registry metric was. It emits metrics without attributes, so live-check exits non-zero on missing required attributes and the test ignores the exit code; attribute conformance stays with `TestLiveCheck_ConformsToSchema`. Emitting conforming attributes for every metric would need per-metric attribute values duplicated from the registry. A missing statistics document fails the test, so a weaver crash cannot pass.

### `EnableOpenMetrics` stays on

Under `NoTranslation`, the OpenMetrics text format types counters as `unknown`, because OpenMetrics requires a `_total` suffix on counters. Prometheus 3.x scrapes protobuf first by default (`scrape_protocols`), which carries the counter type, and plain text keeps `counter`. So the only scrapers that see `unknown` are ones restricted to OpenMetrics text. Turning OpenMetrics off is a separate behavior change outside #14. The spec and README record the limitation.

## Risks / Trade-offs

- \[Existing `/metrics` scrapers and alerts using `_total`/`_seconds` names break\] → pre-1.0, documented in the proposal and README.
- [Backend not configured for UTF-8/NoTranslation stores escaped or suffixed names] → backend requirements documented in manifest, spec and README; not enforceable from the exporter.
- [Live-check JSON report shape changes on a weaver upgrade] → the test fails loudly rather than passing; weaver is pinned.
- [Grafana Cloud per-tenant limits may not be self-service] → documented as a stack-level setting; how to request it is Grafana's process.
