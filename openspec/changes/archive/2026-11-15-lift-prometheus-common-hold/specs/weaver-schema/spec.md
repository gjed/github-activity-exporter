## MODIFIED Requirements

### Requirement: Prometheus exposition verified by live-check

The integration tests SHALL scrape `/metrics`, convert the exposition to live-check samples and run `weaver registry live-check` on them with a policy that fails when a family's exposed type differs from `annotations.prometheus.type`, including for imported metrics. The run SHALL use its own weaver configuration that downgrades the built-in `unexpected_instrument`, `unit_mismatch` and attribute `type_mismatch` findings, since the exposition carries no unit, exposes updowncounters as `gauge` and has only string label values. The `/metrics` run SHALL be made with self-telemetry metrics active and inactive.

#### Scenario: Exposition conforms to the contract

- **WHEN** every registry metric is recorded and `/metrics` is scraped with the Prometheus 3.x default `Accept` header (`application/openmetrics-text;version=1.0.0;escaping=allow-utf-8` first, then `text/plain;version=1.0.0;escaping=allow-utf-8`), or with `text/plain;version=1.0.0;escaping=allow-utf-8` alone
- **THEN** live-check sees exactly the registry metric names, no non-registry metric, and no violation other than a missing required attribute

#### Scenario: Self-telemetry metrics on /metrics

- **WHEN** self-telemetry metrics are active and `/metrics` is scraped
- **THEN** live-check sees the `go.*` and `http.client.*` families, each with the type its `annotations.prometheus.type` declares, and no violation; with self-telemetry metrics inactive no `go.*` or `http.*` family is exposed

#### Scenario: Suffixed name detected

- **WHEN** the exposition exposes `github.pr.automerged` as `github.pr.automerged_total`
- **THEN** live-check exits non-zero and reports the metric as not in the registry

#### Scenario: Wrong exposed type detected

- **WHEN** the exposition types `github.pr.automerged` as `gauge`
- **THEN** live-check exits non-zero with a type-mismatch violation naming `annotations.prometheus.type`
