## Context

OTel Weaver (⭐410, active, Apache 2.0) treats telemetry as a public API. The workflow for this project:

1. Write `registry/attributes.yaml` and `registry/signals.yaml` — all metric names, attribute names, instruments, units
1. `weaver registry check` — validate schema in CI
1. `weaver registry generate` with custom Go templates → `internal/semconv/*.go` (type-safe constants)
1. `weaver registry generate` with Grafana templates → `dashboards/*.json`
1. `weaver registry live-check` — integration test: start exporter, send real OTLP, verify schema compliance

The OTel Go SDK `MeterProvider` is configured with two exporters simultaneously:

- OTLP exporter (primary, required): pushes on each collection cycle
- Prometheus bridge exporter (secondary, optional): serves `/metrics` for pull-based scraping

Both read from the same in-memory metric state — one data model, two views.

## Goals / Non-Goals

**Goals:**

- Schema-first: all metric names locked in Weaver YAML before any collector code is written
- OTLP/HTTP and OTLP/gRPC both supported, selected via config
- Prometheus bridge available as opt-in secondary (no separate registry)
- Weaver codegen for Go constants eliminates magic strings
- Weaver codegen for Grafana dashboard JSON — dashboard is always in sync with schema
- Exporter self-metrics (scrape duration, API errors, OTLP push errors)

**Non-Goals:**

- Traces or logs — metrics only for MVP
- Custom Weaver advisor rules — basic schema validation is sufficient for MVP
- Alerting rules codegen — deferred post-MVP (Weaver supports it but templates need authoring)

## Decisions

### Decision: Weaver registry lives in `registry/` at repo root

Rationale: Conventional location, easy to reference in `weaver` CLI commands and CI. Keeps schema separate from generated code (`internal/semconv/`) and generated artifacts (`dashboards/`).

### Decision: OTel MeterProvider with periodic reader, not on-demand

Rationale: GitHub API data is collected on a configurable interval (default 5m), not on-demand per scrape. A `PeriodicReader` with matching interval pushes OTLP on schedule. The Prometheus bridge uses a separate `ManualReader` that reads the same metric state on each HTTP scrape — no re-querying the GitHub API.

```
┌─────────────────────────────────────────────────────────────┐
│                 METER PROVIDER WIRING                       │
└─────────────────────────────────────────────────────────────┘

  MeterProvider
  ├── PeriodicReader (interval: collection_interval)
  │   └── OTLP Exporter (HTTP or gRPC)  ← primary
  └── ManualReader
      └── Prometheus Bridge Exporter    ← secondary (opt-in)
              │
              └── /metrics HTTP handler
```

### Decision: OTLP/HTTP as default, gRPC as opt-in

Rationale: OTLP/HTTP works through most firewalls and proxies without extra configuration. gRPC offers better performance for high-volume scenarios but adds complexity. Default to HTTP; select gRPC via `export.otlp.protocol: grpc`.

### Decision: Custom Go codegen template for Weaver

Rationale: Weaver ships Rust templates in its examples; Go templates need to be written. The template is small (~50 lines of Jinja2) and produces a `semconv.go` file with typed constants. This is a one-time ~2h investment that pays off in type safety across all collector code.

### Decision: Grafana dashboard generated from Weaver schema

Rationale: If the dashboard is hand-written, it drifts from metric names as the schema evolves. A Jinja2 template in `templates/grafana/` generates `dashboards/renovate-github.json` from the resolved schema. Running `make generate` regenerates it — the dashboard is always in sync.

## Risks / Trade-offs

- **Weaver Go template authoring** → Mitigation: the basic example ships Rust templates; Go adaptation is straightforward. Timebox to 2h; if blocked, use string constants initially and regenerate later
- **OTLP endpoint required for primary output** → Mitigation: provide a `docker-compose.yaml` with Grafana Agent or OTel Collector for local dev; document clearly that a collector or Grafana Cloud OTLP endpoint is needed
- **Prometheus bridge adds a dependency** → It's part of the OTel Go SDK contrib; minimal overhead; disabled by default

## Open Questions

- What Weaver registry manifest URL? → Use `https://github.com/gjed/renovate-exporter/schemas/<version>` as `schema_url`; can be a placeholder for MVP
- Should `live-check` run in unit tests or only integration tests? → Integration tests (requires live OTLP receiver); unit tests use table-driven metric assertions
