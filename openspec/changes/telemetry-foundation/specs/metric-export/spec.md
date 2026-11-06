# Spec Delta

## ADDED Requirements

### Requirement: Shared resource and instrumentation scope

Product metrics SHALL carry the resource shared with self-telemetry: `service.name` (default `github-activity-exporter`), `service.version` (the build version) and `service.instance.id` (a UUID generated at startup), with `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` overriding them per the OTel spec. Every meter SHALL use the scope `github.com/gjed/github-activity-exporter` versioned with the build version.

#### Scenario: Default resource

- **WHEN** product metrics are exported with no `OTEL_*` resource variable set
- **THEN** the resource carries `service.name=github-activity-exporter`, `service.version` equal to the version `-version` prints, and a UUID `service.instance.id`

#### Scenario: Env overrides resource

- **WHEN** `OTEL_SERVICE_NAME=gh-exp` and `OTEL_RESOURCE_ATTRIBUTES=service.instance.id=pod-1` are set
- **THEN** the resource carries `service.name=gh-exp` and `service.instance.id=pod-1`

#### Scenario: One scope for all targets

- **WHEN** two targets export metrics
- **THEN** every metric has scope name `github.com/gjed/github-activity-exporter` and scope version equal to the build version
