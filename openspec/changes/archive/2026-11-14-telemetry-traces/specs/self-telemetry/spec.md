# Spec Delta

## ADDED Requirements

### Requirement: Collection cycle traces

With traces active, the system SHALL record one root span `github_exporter.cycle` per target cycle, with `exporter.target`, `github_exporter.cycle.outcome` (`success`, `partial`, `failure`) and, after a successful discovery, `github_exporter.cycle.repos`. Its children SHALL be one `github_exporter.collect` span per collector (`github_exporter.collector`, `github_exporter.failed_repos`) and one `github_exporter.flush` span.

#### Scenario: Cycle span tree

- **WHEN** a cycle of a target with the PR, issue and dashboard collectors completes
- **THEN** its trace has one `github_exporter.cycle` root span with three `github_exporter.collect` children and one `github_exporter.flush` child

#### Scenario: Discovery fails

- **WHEN** repo discovery fails in a cycle
- **THEN** the cycle span has `github_exporter.cycle.outcome` `failure`, status `Error` and `error.type`, and no collector spans

#### Scenario: Some repos fail

- **WHEN** discovery succeeds and at least one repo fails in a collector
- **THEN** the cycle span has `github_exporter.cycle.outcome` `partial` and that collector span has status `Error` and the number of failed repos

### Requirement: Worker pool unit spans

With traces active, the system SHALL record one span per started unit of the target's worker pool, as a child of its collector span whatever goroutine runs it: `github_exporter.repo` (`github.org`, `github.repo`) per PR or issue title-filter repo, `github_exporter.batch` (`github_exporter.batch.size`, `github_exporter.failed_repos`) per GraphQL batch. A failed unit SHALL have status `Error` and `error.type`; a unit that never starts, no span.

#### Scenario: Concurrent units stay in the cycle trace

- **WHEN** a cycle runs with `collection.concurrency` 4
- **THEN** every repo and batch span has the cycle's trace ID and its collector span as parent, and no unit span is a root

#### Scenario: Failed repo in a batch

- **WHEN** GitHub answers a repo of a batched issue count query with a GraphQL `NOT_FOUND` error
- **THEN** that batch's `github_exporter.batch` span has status `Error`, `github_exporter.failed_repos` 1 and `error.type` `NOT_FOUND`

### Requirement: GitHub HTTP client spans

With traces active, every GitHub round trip SHALL be one semconv `http.client` span named `{method} {url.template}`, or `{method}` outside the known routes, with `url.template`, `graphql.operation.type` and `graphql.operation.name` on GraphQL calls (the name when the document has one), and `http.request.resend_count` on retries. Span names SHALL NOT contain a full URL, org, repo or installation. The transport SHALL NOT send trace context to GitHub.

#### Scenario: GraphQL batch request

- **WHEN** the issue collector sends a batched issue count query
- **THEN** its client span is named `POST /graphql` and carries `url.template` `/graphql`, `graphql.operation.type` `query` and `graphql.operation.name` `IssueCountBatch`

#### Scenario: REST discovery request

- **WHEN** discovery requests `/orgs/zextras/repos?page=2`
- **THEN** the client span is named `GET /orgs/{org}/repos`

#### Scenario: Retried request

- **WHEN** a request is sent again after a rate limit response
- **THEN** the second attempt is its own client span with `http.request.resend_count` 1

### Requirement: Rate-limit wait spans

With traces active, each wait of a request for a paused pool or a secondary rate limit pause SHALL be a `github_exporter.rate_limit.wait` span in the request's context, lasting as long as the wait, with `github.api.rate_limit.resource` (the request's pool) and `github_exporter.rate_limit.reason` `primary` or `secondary`. A request that does not wait SHALL record no wait span.

#### Scenario: Primary pool paused

- **WHEN** a request to the `graphql` pool waits for the pool's window reset
- **THEN** its unit span has a `github_exporter.rate_limit.wait` child with `github.api.rate_limit.resource` `graphql` and `github_exporter.rate_limit.reason` `primary`

#### Scenario: Secondary pause

- **WHEN** a secondary rate limit pauses the target while other units have requests pending
- **THEN** each held request records a wait span with `github_exporter.rate_limit.reason` `secondary`

### Requirement: Error recording on spans

The system SHALL mark a failed span with status `Error`, a description and a bounded `error.type`: the GraphQL error type when GitHub returned one, the HTTP status code for a REST error response, `_OTHER` otherwise. It SHALL NOT record exceptions as span events, as semconv 1.41.1 deprecates exceptions on spans.

#### Scenario: No exception events

- **WHEN** a repo fails
- **THEN** its span has no span events

### Requirement: No tracing of the exporter's own export

The system SHALL NOT create spans for its own OTLP exports of metrics, traces or logs.

#### Scenario: Flush has no children

- **WHEN** the per-cycle flush pushes metrics over OTLP with traces active
- **THEN** the `github_exporter.flush` span has no child spans and no other span is recorded for the export

### Requirement: Trace sampling

The system SHALL sample traces with the SDK sampler from `OTEL_TRACES_SAMPLER` and `OTEL_TRACES_SAMPLER_ARG`, default `parentbased_always_on`, so a cycle trace is kept or dropped whole.

#### Scenario: Ratio sampler

- **WHEN** `OTEL_TRACES_SAMPLER=parentbased_traceidratio` and `OTEL_TRACES_SAMPLER_ARG=0.1`
- **THEN** about one cycle in ten is exported, each with all its spans

### Requirement: Traces off cost nothing

With traces inactive, the system SHALL use the no-op tracer everywhere, SHALL NOT read GraphQL request bodies for span attributes, and SHALL leave the GitHub transport and the exported metrics as they are with traces active.

#### Scenario: Metric output independent of traces

- **WHEN** the same cycle runs with traces active and inactive
- **THEN** OTLP and `/metrics` carry the same metric names, attributes and values
