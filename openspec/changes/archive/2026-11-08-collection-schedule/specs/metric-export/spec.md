# Spec Delta

## MODIFIED Requirements

### Requirement: Configurable collection interval

The system SHALL start a collection cycle per target every `collection_interval`, measured from the start of the previous cycle, and push its metrics via OTLP at the end of the cycle. When a cycle runs longer than `collection_interval`, the next cycle SHALL start as soon as it finishes: cycles of one target never overlap and missed periods are not caught up.

#### Scenario: Default collection interval

- **WHEN** `collection_interval` is not configured
- **THEN** metrics are collected and pushed every 5 minutes

#### Scenario: Custom interval respected

- **WHEN** `collection_interval: 15m` is set
- **THEN** collection and OTLP push occur approximately every 15 minutes

#### Scenario: Period measured from cycle start

- **WHEN** `collection_interval` is `15m` and a cycle starting at 12:00 takes 10 minutes
- **THEN** the next cycle starts at 12:15

#### Scenario: Overrunning cycle

- **WHEN** `collection_interval` is `15m` and a cycle starting at 12:00 takes 17 minutes
- **THEN** the next cycle starts at 12:17, a warning is logged naming the target, and `github_exporter.scrape.overruns` is incremented for the target

#### Scenario: No catch-up after an overrun

- **WHEN** `collection_interval` is `15m`, a cycle starting at 12:00 takes 40 minutes and the next one takes 1 minute
- **THEN** the next cycle starts at 12:40 and the one after it at 12:55

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

#### Scenario: Overrun counter incremented

- **WHEN** a collection cycle runs longer than `collection_interval`
- **THEN** `github_exporter.scrape.overruns` counter is incremented with the `exporter.target` attribute
