# health-endpoints Specification

## Purpose

Expose liveness and readiness HTTP endpoints so orchestrators can detect an exporter that never became ready, has a dead or stuck collection loop, or holds a revoked GitHub credential.

## Requirements

### Requirement: Always-on health server

The system SHALL always serve `/healthz` and `/readyz` over HTTP on `health.listen_address` (default `:10062`), independent of whether the Prometheus bridge is enabled. The server SHALL start before the startup credential checks. Both endpoints SHALL answer `GET` and `HEAD`.

#### Scenario: Default listen address

- **WHEN** `health.listen_address` is not set
- **THEN** `/healthz` and `/readyz` are served on `:10062`

#### Scenario: Custom listen address

- **WHEN** `health.listen_address: ":8081"` is configured
- **THEN** `/healthz` and `/readyz` are served on `:8081`

#### Scenario: Served with the bridge disabled

- **WHEN** `export.prometheus.enabled` is `false`
- **THEN** `/healthz` and `/readyz` are still served

#### Scenario: Invalid thresholds

- **WHEN** `health.stall_timeout` is negative, or is set and not longer than `collection_interval`, or `health.unauthorized_cycles` is less than 1
- **THEN** the exporter fails to start with a config error naming the key

#### Scenario: Address clash with the bridge

- **WHEN** the bridge is enabled and `health.listen_address` equals `export.prometheus.listen_address`
- **THEN** the exporter fails to start with a config error naming both keys

### Requirement: Response format

Each endpoint SHALL return HTTP 200 with body `ok` when its check passes, and HTTP 503 with one line per failing target, naming the target and the reason, when it fails.

#### Scenario: Failing response names the target

- **WHEN** target `acme` has not completed a collection cycle and `/readyz` is requested
- **THEN** the response is 503 and the body contains a line naming `acme` and the reason

### Requirement: Readiness

`/readyz` SHALL return 200 only when every configured target has passed its startup credential check and completed at least one collection cycle in which repo discovery succeeded. Failures of individual repos within that cycle SHALL NOT block readiness. Once ready, the exporter SHALL stay ready. HTTP 401 responses SHALL NOT affect readiness.

#### Scenario: Not ready during startup

- **WHEN** a target's credential check or first collection cycle has not finished
- **THEN** `/readyz` returns 503

#### Scenario: Ready after every target collected once

- **WHEN** every target has completed a cycle with successful repo discovery
- **THEN** `/readyz` returns 200

#### Scenario: Discovery failure does not count

- **WHEN** a target's only completed cycle failed repo discovery
- **THEN** `/readyz` returns 503

### Requirement: Liveness of collection loops

`/healthz` SHALL return 503 when any target's collection loop has exited before shutdown, or has not completed a collection cycle (any outcome) within the stall timeout of its previous completed cycle or, before the first, of the loop start. The stall timeout is `health.stall_timeout`, default `2 × collection_interval + 1h`: a completion follows the previous one by at most `collection_interval` plus the duration of the cycle in progress, so the default allows a cycle of up to `collection_interval + 1h`. It SHALL return 200 during startup, before loops run.

#### Scenario: Healthy during startup

- **WHEN** the startup credential checks are still running
- **THEN** `/healthz` returns 200

#### Scenario: Stalled loop

- **WHEN** `collection_interval` is `5m`, `health.stall_timeout` is not set, and a target's last completed cycle was more than 1h10m ago
- **THEN** `/healthz` returns 503 naming the target as stalled

#### Scenario: Custom stall timeout

- **WHEN** `health.stall_timeout: 30m` is configured and a target's last completed cycle was more than 30m ago
- **THEN** `/healthz` returns 503 naming the target as stalled

#### Scenario: Exited loop

- **WHEN** a target's collection loop returns while the exporter is not shutting down
- **THEN** `/healthz` returns 503 naming the target

#### Scenario: Overrunning cycles stay healthy

- **WHEN** `collection_interval` is `5m`, `health.stall_timeout` is not set, and every cycle takes 30 minutes
- **THEN** `/healthz` returns 200

### Requirement: Liveness on rejected credentials

A collection cycle SHALL count as unauthorized when GitHub answered at least one request of the target with HTTP 401, including the GitHub App installation-token exchange, and answered none with any other status. `/healthz` SHALL return 503 when a target has N unauthorized cycles in a row, where N is `health.unauthorized_cycles`, default 3. A cycle in which GitHub answered any request with a non-401 status SHALL reset the count. A cycle with no GitHub response SHALL leave it unchanged.

#### Scenario: Revoked credential

- **WHEN** `health.unauthorized_cycles` is not set and every GitHub request of a target returns 401 for 3 consecutive cycles
- **THEN** `/healthz` returns 503 naming the target as unauthorized, and `/readyz` is unchanged

#### Scenario: Below threshold

- **WHEN** `health.unauthorized_cycles` is not set and a target has 2 consecutive unauthorized cycles
- **THEN** `/healthz` returns 200

#### Scenario: Custom unauthorized threshold

- **WHEN** `health.unauthorized_cycles: 1` is configured and a target completes one unauthorized cycle
- **THEN** `/healthz` returns 503 naming the target as unauthorized

#### Scenario: Isolated 401

- **WHEN** a cycle gets a 401 on one request and 200 on others
- **THEN** the cycle is not unauthorized and resets the count

#### Scenario: Cycle without GitHub responses

- **WHEN** a target has 2 unauthorized cycles, then a cycle with no GitHub response, then an unauthorized cycle, with `health.unauthorized_cycles` not set
- **THEN** `/healthz` returns 503 naming the target as unauthorized

#### Scenario: Recovery

- **WHEN** a target over the unauthorized threshold completes a cycle in which GitHub answered a request with a non-401 status
- **THEN** `/healthz` returns 200 for that target
