## MODIFIED Requirements

### Requirement: Exporter self-metrics

The system SHALL emit metrics about its own operation using the same OTel MeterProvider and OTLP pipeline.

#### Scenario: Scrape duration recorded

- **WHEN** a collection cycle completes
- **THEN** `github_exporter.scrape.duration` histogram is updated with the cycle duration in seconds

#### Scenario: API error counter incremented

- **WHEN** a GitHub API call returns an error
- **THEN** `github_exporter.api.errors` counter is incremented with `exporter.target` and `github.api.endpoint` attributes

#### Scenario: OTLP error counter incremented

- **WHEN** an OTLP push fails
- **THEN** `github_exporter.otlp.errors` counter is incremented
