## MODIFIED Requirements

### Requirement: Prometheus bridge as optional secondary

The system SHALL optionally expose a `/metrics` HTTP endpoint in Prometheus text exposition format, implemented via the OTel Prometheus bridge exporter (not a separate metric registry). The bridge SHALL expose metric names per the registry naming contract: each metric family name equals the registry metric name exactly, with no suffix and, when the scraper accepts UTF-8 names, no escaping, and histogram samples add only `_bucket`, `_sum` and `_count`. The endpoint SHALL NOT serve the OpenMetrics text format, which requires a `_total` suffix on counter names; a scraper that asks for OpenMetrics first SHALL receive Prometheus text format with counters typed `counter`. The name escaping SHALL be the `escaping` parameter of the first `Accept` clause that matches a served text format. A request whose `Accept` header matches no served format, or that has none, SHALL receive Prometheus text 0.0.4 with underscore-escaped names (`github_pr_count`).

#### Scenario: Prometheus endpoint disabled by default

- **WHEN** `export.prometheus.enabled` is not set or is `false`
- **THEN** no HTTP server for `/metrics` is started

#### Scenario: Prometheus endpoint enabled

- **WHEN** `export.prometheus.enabled: true` is configured
- **THEN** a GET request to `/metrics` returns HTTP 200 with Prometheus text format exposing the same metrics as the OTLP output

#### Scenario: Default listen address

- **WHEN** `export.prometheus.enabled: true` is configured and `export.prometheus.listen_address` is not set
- **THEN** the `/metrics` server listens on `:9090`

#### Scenario: Custom listen address

- **WHEN** `export.prometheus.listen_address: ":9464"` is configured with the bridge enabled
- **THEN** the `/metrics` server listens on `:9464`

#### Scenario: Prometheus bridge reads same metric state as OTLP

- **WHEN** both OTLP and Prometheus outputs are enabled
- **THEN** both expose identical metric values (no separate collection; one MeterProvider, two exporters)

#### Scenario: Exposed names equal registry names

- **WHEN** every registry metric is recorded and `/metrics` is scraped
- **THEN** the set of metric family names equals the set of registry metric names, and every sample name is its family name, or for histograms the family name plus `_bucket`, `_sum` or `_count`

#### Scenario: OpenMetrics scrape keeps counter types

- **WHEN** `/metrics` is scraped with the Prometheus 3.x default `Accept` header, which lists `application/openmetrics-text;version=1.0.0;escaping=allow-utf-8` first and also `text/plain;version=1.0.0;escaping=allow-utf-8`
- **THEN** the response uses the Prometheus text format 1.0.0, the family names equal the registry names, and registry counters are typed `counter`

#### Scenario: OpenMetrics-only scrape

- **WHEN** `/metrics` is scraped with `Accept: application/openmetrics-text;version=1.0.0;escaping=allow-utf-8` and no other clause
- **THEN** the response uses the Prometheus text format 0.0.4 with underscore-escaped names, for example `github_pr_count`

#### Scenario: Legacy scrape

- **WHEN** `/metrics` is scraped with no `Accept` header or with `Accept: text/plain;version=0.0.4`
- **THEN** the response uses the Prometheus text format 0.0.4 with underscore-escaped names, for example `github_pr_count`

#### Scenario: No suffixes added

- **WHEN** `github.pr.automerged` (counter) and `github.pr.age` (unit `s`) are recorded and `/metrics` is scraped
- **THEN** the exposition contains `github.pr.automerged` and `github.pr.age`, and no `github.pr.automerged_total` or `github.pr.age_seconds`
