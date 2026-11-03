## MODIFIED Requirements

### Requirement: Prometheus bridge as optional secondary

The system SHALL optionally expose a `/metrics` HTTP endpoint in Prometheus text exposition format, implemented via the OTel Prometheus bridge exporter (not a separate metric registry). The bridge SHALL expose metric names per the registry naming contract: each metric family name equals the registry metric name exactly, with no suffix and no escaping, and histogram samples add only `_bucket`, `_sum` and `_count`. When a scraper negotiates the OpenMetrics text format, counters are typed `unknown` because their names carry no `_total` suffix; Prometheus text and protobuf formats keep the `counter` type.

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

#### Scenario: No suffixes added

- **WHEN** `github.pr.automerged` (counter) and `github.pr.age` (unit `s`) are recorded and `/metrics` is scraped
- **THEN** the exposition contains `github.pr.automerged` and `github.pr.age`, and no `github.pr.automerged_total` or `github.pr.age_seconds`
