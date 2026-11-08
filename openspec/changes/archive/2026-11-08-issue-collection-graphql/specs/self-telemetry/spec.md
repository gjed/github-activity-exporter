# Spec Delta

## MODIFIED Requirements

### Requirement: GitHub HTTP client metrics

When self-telemetry metrics are active, every HTTP request the exporter sends to GitHub, including the credential check and the App token exchange, SHALL be recorded as `http.client.request.duration` and `http.client.request.body.size`. The duration SHALL cover one HTTP round trip, without rate-limit or token waits. When self-telemetry metrics are inactive, no `http.*` metric SHALL be exported and the GitHub HTTP transport SHALL be the uninstrumented one.

#### Scenario: REST and GraphQL calls recorded

- **WHEN** self-telemetry metrics are active and a collection cycle lists the org's repositories over REST and queries pull requests and issues over GraphQL
- **THEN** `http.client.request.duration` has data points with `http.request.method`, `http.response.status_code`, `server.address`, `server.port` and `url.template` `/orgs/{org}/repos` for the REST call and `/graphql` for the GraphQL calls

#### Scenario: App token exchange recorded

- **WHEN** self-telemetry metrics are active and a GitHub App target exchanges its JWT for an installation token
- **THEN** `http.client.request.duration` has a data point with `http.request.method` `POST` and `url.template` `/app/installations/{installation_id}/access_tokens`

#### Scenario: Error status

- **WHEN** GitHub answers a request with a 4xx or 5xx status
- **THEN** the data point carries `http.response.status_code` and `error.type` set to the status code; a request that fails without a response carries `error.type` and no `http.response.status_code`

#### Scenario: HTTP metrics with the signal off

- **WHEN** self-telemetry metrics are inactive
- **THEN** neither the OTLP push nor `/metrics` carries any `http.*` metric

### Requirement: Bounded HTTP client attributes

HTTP client metric attributes SHALL have bounded cardinality. `url.template` SHALL be set only for the routes the exporter calls, as an absolute path template under the target's API root (for example `/api/v3/orgs/{org}/repos` on GitHub Enterprise Server), and omitted for any other path. No attribute SHALL carry a full URL, a query string, an org, user, repo or installation identifier, or a token.

#### Scenario: Repo names stay out of attributes

- **WHEN** the exporter lists the repositories of `acme` and queries `acme/widgets` with self-telemetry metrics active
- **THEN** no `http.client.request.duration` attribute value contains `acme` or `widgets`, and `url.template` is `/orgs/{org}/repos` or `/graphql`

#### Scenario: Unknown route

- **WHEN** the exporter sends a request to a path that is not one of its known routes
- **THEN** the data point has no `url.template` attribute
