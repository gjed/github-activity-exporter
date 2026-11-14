# Spec Delta

## ADDED Requirements

### Requirement: Span schema

The registry SHALL declare every span the exporter creates: its own spans as `definition/2` span groups with their attributes, and GitHub HTTP client spans by reference to the semconv `http.client` span of the pinned dependency, with the GraphQL operation attributes referenced from the dependency.

#### Scenario: Registry check

- **WHEN** `make check-schema` runs
- **THEN** it validates the span groups with the metrics

### Requirement: Span live-check

The live-check integration tests SHALL export the spans of collection cycles to `weaver registry live-check`, pair each span with its span group through `.weaver.toml` matchers, and fail on any violation or on any span sample that matches no span group.

#### Scenario: Undeclared span

- **WHEN** the exporter emits a span whose name matches no matcher
- **THEN** the span live-check test fails
