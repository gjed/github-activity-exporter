## Why

The metric contract for this exporter must be defined before any collection code is written. Without schema-first design, metric names drift from documentation, dashboards break on refactors, and alert rules become impossible to validate. OTel Weaver enforces "observability by design": the registry YAML is the single source of truth for every metric name, attribute, unit, and instrument type — Go constants, Grafana dashboard JSON, and alert rules are all generated from it.

This change also establishes the OTLP-first export pipeline. OTLP (HTTP or gRPC) is the primary output; Prometheus `/metrics` is an optional secondary via the OTel Prometheus bridge — no separate registry, one data model with two views.

## What Changes

- New `registry/` directory: OTel Weaver semantic convention YAML files defining all metrics and attributes for this exporter
- `weaver registry check` runs in CI on every PR — schema errors are caught before code
- `weaver registry generate` produces Go constants (`internal/semconv/`) and Grafana dashboard JSON (`dashboards/`)
- OTel Go `MeterProvider` wired with OTLP exporter (HTTP + gRPC, selectable) as primary
- Optional Prometheus `/metrics` endpoint via OTel Prometheus bridge exporter
- Exporter self-metrics: scrape duration, API error counters, OTLP push errors

## Capabilities

### New Capabilities

- `weaver-schema`: Weaver registry YAML, manifest, CI validation, codegen templates for Go constants and Grafana dashboard
- `metric-export`: OTel MeterProvider with OTLP primary and Prometheus bridge secondary; configurable scrape interval; exporter self-metrics

### Modified Capabilities

## Impact

- New directory: `registry/` (Weaver semantic conventions)
- New directory: `internal/semconv/` (generated Go constants — do not edit by hand)
- New directory: `dashboards/` (generated Grafana JSON)
- New dependency: OTel Go SDK (`go.opentelemetry.io/otel`, `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc`, `otlpmetrichttp`, `exporters/prometheus`)
- Blocks: `data-collectors` (needs metric names before collectors can record)
- Parallel with: `github-client`, `ci-pipeline`
