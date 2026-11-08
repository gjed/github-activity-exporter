# Spec Delta

## MODIFIED Requirements

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
