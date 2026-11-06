# Design

## Context

Weaver 0.28 drops unknown manifest keys, so the contract cannot live in `registry/manifest.yaml`. Metric annotations survive resolution: with `--v2` they reach `after_resolution` policies under `input.registry.metrics[].annotations`, and live-check advice policies under `input.registry_group.annotations`. Both paths were checked on `otel/weaver:v0.28.0`.

## Goals / Non-Goals

**Goals:**

- One machine-readable contract per metric, checked at registry time and against the real `/metrics` output.

**Non-Goals:**

- Reading the annotations from templates (dashboard generation is [#19](https://github.com/gjed/github-activity-exporter/issues/19)).
- Verifying units on `/metrics`: the exposition carries none. Units stay verified on OTLP.

## Decisions

- **Explicit annotation on every metric, not derived in a template.** The issue asks for a contract tooling reads; the policy guarantees it never drifts from the name and instrument. Alternative: derive the Prometheus name/type in templates. Rejected: nothing for tooling to read and nothing for Weaver to check `/metrics` against.
- **Registry policy findings.** Package `after_resolution`, level `violation`, ids `prometheus_annotation_missing` (no `prometheus.name` or `prometheus.type`), `prometheus_name_mismatch`, `prometheus_type_mismatch`. `weaver registry check` exits non-zero on any of them.
- **Exposition to live-check JSON in Go.** A converter in `tests/integration` parses the scrape with `expfmt` (UTF-8 names) and writes a JSON array: a `resource` sample from the `target_info` labels, then per scope an `instrumentation_scope` sample (`otel_scope_name`/`_version`/`_schema_url`, other `otel_scope_*` labels as scope attributes) followed by that scope's metrics. A metric sample carries the family name, `instrument` = the exposed Prometheus type (`counter`, `gauge`, `histogram`), `unit` = `""`, and one data point per label set; labels become string attributes. The bridge writes resource attributes into `target_info` as strings, an int as its decimal form and a string slice as a JSON array, so the converter decodes those two back (`process.pid` is int, `process.command_args` is string[]). A classic histogram becomes one histogram data point per label set: `explicit_bounds` from the finite `le` values, `bucket_counts` de-cumulated, plus `count` and `sum`. Live-check pairs scope and resource with the metrics that follow them, so ordering carries the association.
- **Type check in Rego.** `policies/live_check/` holds a `live_check_advice` policy: for a metric sample with `input.registry_group.annotations.prometheus.type`, a `violation` `prometheus_type_mismatch` when the sample's `instrument` differs. A name not in the registry (`github.pr.automerged_total`) gets the built-in `missing_metric` violation, no extra rule needed.
- **Dedicated config `.weaver-prometheus.toml`.** It repeats the resource matcher from `.weaver.toml` and adds `finding_level_overrides` that set `unexpected_instrument` and `unit_mismatch` on metrics to `information`: an updowncounter is exposed as `gauge` and the exposition has no unit, and the Rego type check replaces both. The test passes `--advice-policies policies/live_check/`, which replaces the default OTel advice policies for this run only. The OTLP run is unchanged.
- **What the `/metrics` test asserts.** Like the OTLP names test, it records every registry metric once without attributes, so `required_attribute_not_present` is expected. It passes when every registry metric was seen, no non-registry metric was seen, and the report has no violation other than `required_attribute_not_present`. Attribute conformance stays with the OTLP conformance test. It scrapes twice, with the OpenMetrics-first `Accept` header Prometheus 3.x and Alloy send and with plain text, which keeps the coverage `naming_test.go` had of the fallback to the text format: `/metrics` does not serve OpenMetrics, so both scrapes check the same encoding.
- **Negative tests mutate the parsed scrape**, not the exporter: rename `github.pr.automerged` to `github.pr.automerged_total` (expects `Metric does not exist in the registry.`), and retype it `gauge` (expects the `prometheus_type_mismatch` message). The exporter cannot be configured to emit suffixes, so this is the only way to feed Weaver a bad exposition from a real scrape.
- **OTLP names test.** Shares the "no violation other than `required_attribute_not_present`" check with the `/metrics` test.

## Risks / Trade-offs

- \[`/metrics` coverage needs Docker or weaver\] -> The run is in the integration suite, which CI runs on every PR to main; `go test ./...` no longer checks `/metrics` names.
- [Converter bug masks a real exposition change] -> The negative tests go through the same converter and must fail.
- \[`.weaver-prometheus.toml` duplicates the resource matcher\] -> Two lines of TOML; a header comment says to keep them in sync with `.weaver.toml`.
