# Design

## Context

- `internal/github/client.go`: `rateLimitTransport` parses `X-RateLimit-Remaining` and `X-RateLimit-Reset` on every response, stores a `RateLimitState{Remaining, ResetAt}` and calls the registered `RateLimitHook`s. The hook runs inside the transport and must not block. `WithRateLimitHook` has no caller.
- `cmd/github-activity-exporter/main.go` builds one client per target in `buildTargetRunner`. `SelfMetrics` is created once on the global MeterProvider and reaches runners only through a `RunnerOption`.
- The credential ping goes through the authenticator, not the client transport, so the client sees no rate-limit headers until its first API call.

## Goals / Non-Goals

**Goals:**

- Per-target gauges that report the client's last observed rate-limit state.

**Non-Goals:**

- Splitting the state by rate-limit pool (core, graphql, search); see Risks.
- Changing the pause logic or the client.
- Dashboard panel design beyond what the generator renders.

## Decisions

### Synchronous gauges recorded from the hook

The hook records `Int64Gauge`s on each response. The SDK keeps the last value per attribute set, so every export carries the latest state. Alternative: observable gauges whose callback reads `Client.RateLimitState()`. Rejected: the issue asks for the hook, and a callback reports `0` remaining before the first response, which reads as "exhausted".

### `reset` is the Unix reset time, not seconds until reset

The hook fires only on API responses. A "seconds until reset" value would freeze between responses and go stale; the absolute reset time stays correct. Unit `s`, as the registry naming contract keeps the name unsuffixed. When the client applies its 60 s fallback (low remaining, missing `X-RateLimit-Reset`), the gauge reports that fallback time.

### `SelfMetrics.RecordRateLimit(ctx, target, remaining, resetAt)`

Keeps `internal/exporter` free of an `internal/github` import. `main.go` passes `selfMetrics` to `buildTargetRunner`, which wraps it in the hook with `context.Background()` (the hook has no context) and the target name, the same value runners use for `exporter.target`.

### Only `exporter.target`

Matches the scenario ("per target") and the other self-metrics. No pool attribute; see Risks.

## Risks / Trade-offs

- [Mixed pools] The transport stores headers from REST (core pool) and GraphQL (graphql pool) responses in one state, so the gauges, like the existing pause logic, show whichever pool answered last. → Out of scope for [#16](https://github.com/gjed/github-activity-exporter/issues/16). Fixing it needs per-pool state in the client (`X-RateLimit-Resource`) and a pool attribute; follow-up issue.
- [No series before the first API call] A target that never reaches the API (for example, an empty org list) has no rate-limit series. → Accepted; no fake values.
