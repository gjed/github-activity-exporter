# Design

## Context

See proposal.md for motivation. Today `main` loads config, builds the OTLP provider, pings every target's credential (exit on failure), then starts one `Runner.Run` goroutine per target. `Run` loops `runOnce` then sleeps `collection_interval`; repo discovery failure ends a cycle early, per-repo collector failures are logged and counted. Every GitHub request goes through `rateLimitTransport` in `internal/github/client.go`, which may block until the rate-limit window resets (up to 1h). The GitHub App installation-token exchange uses `http.DefaultClient` directly, outside that transport, and returns an untyped error.

## Goals / Non-Goals

**Goals:**

- Probe semantics and thresholds exactly as in `specs/health-endpoints/spec.md`.
- One source of health state, cheap to read from HTTP handlers.

**Non-Goals:**

- Readiness that flips back to not-ready. The exporter takes no inbound traffic, so readiness only gates "has produced data once".

## Decisions

### New `internal/health` package with a `Tracker`

`Tracker` holds one state struct per target behind a mutex: credential checked, loop started-at, last completed cycle time, ready flag, loop-exited flag, consecutive unauthorized cycles, and the current cycle's 401 / non-401 response counters. It exposes `Handler()` serving `/healthz` and `/readyz`, and an injectable clock for tests. Main registers every target before the pings so `/readyz` lists unchecked targets. Alternative: compute health from self-metrics. Rejected: OTel instruments are write-only from the app side.

### 401 detection at the HTTP layer

`internal/github` gets a client option `WithAuthResultHook(func(unauthorized bool))`, called once per GitHub HTTP response in `rateLimitTransport` (`unauthorized = status == 401`). For App auth, `fetchInstallationToken` wraps a 401 in a new exported sentinel `ErrUnauthorized`; the token source calls the hook with `true` when the error matches. Only 401 counts: 403 also means rate limit or missing permission, and the issue scopes this to revoked or invalid credentials. Alternative: inspect errors returned by collectors. Rejected: errors are swallowed per repo and go-github, githubv4 and the token source wrap them differently.

### Cycle classification in the tracker, not the runner

The runner calls an optional observer: `CycleStarted()` resets the response counters, `CycleFinished(discoveryOK bool)` records the completion time, sets ready when `discoveryOK`, and classifies the cycle as unauthorized when 401s > 0 and non-401s == 0. Any other outcome resets the consecutive count. Default threshold 3 (`health.unauthorized_cycles`): at the default 5m interval a revoked credential fails `/healthz` after about 15 minutes, while a single bad cycle (GitHub incident) does not restart the pod. A restarted pod with a revoked credential then fails the startup ping and crash-loops, which is the intended visible signal.

### Stall timeout default `2 × collection_interval + 1h`

`health.stall_timeout`; when unset or `0` it defaults to the formula. Measured from the last completed cycle (or loop start). One interval of sleep, one interval of normal cycle work, plus 1h for a full primary rate-limit pause in `waitIfPaused`. A shorter bound would restart pods that are only waiting out the rate limit; users who want faster detection set it explicitly. Checked lazily at request time; no watchdog goroutine.

### Exited loop

`main` wraps `r.Run(ctx)` and calls `LoopExited` when it returns while `ctx` is not cancelled. A panic already kills the process, so this only guards a future early return.

### Server lifecycle

`main` starts the health server right after config load, before the OTLP provider and the credential pings, and shuts it down (5s timeout) after the runners stop. `ReadTimeout`/`WriteTimeout` 10s like the bridge. A listen failure is fatal at startup: bind synchronously with `net.Listen`, then `Serve` in a goroutine.

### Bridge `/healthz` removed

Keeping a second, always-200 `/healthz` on `:9090` would contradict the real one. The bridge serves only `/metrics`.

### Config

`health.listen_address`, default `:10062`: the [Prometheus default port allocations](https://github.com/prometheus/prometheus/wiki/Default-port-allocations) list is fully allocated from 9100 to 10061, so 10062 is the first free exporter port. Registering it on the wiki is a manual follow-up outside this PR. Set via viper default, so `GITHUB_ACTIVITY_EXPORTER_HEALTH_LISTEN_ADDRESS` overrides it. `health.stall_timeout` (duration, default `0` meaning the formula; negative rejected) and `health.unauthorized_cycles` (int, default 3; below 1 rejected). When the bridge is enabled, the two listen addresses must differ. No `enabled` flag: the server is always on.

## Risks / Trade-offs

- [A 1h10m stall bound is slow to catch a real deadlock] → Correctness over speed: a faster bound kills pods during legitimate rate-limit pauses.
- \[Discovery is cached for `refresh_interval`; a cycle may issue few requests\] → Collectors still call the API every cycle for each discovered repo, so a revoked credential still yields 401s. A target with no repos only calls GitHub on discovery refresh (default 60m), so it reaches the threshold after N refreshes.
- [A cycle with zero GitHub responses (network down, or a target with no repos between discovery refreshes)] → It leaves the count unchanged. Resetting would let a target with no repos, which only calls GitHub on discovery refresh, never reach the threshold; counting it would turn network outages into credential failures.
- [Port 10062 may clash with something else in the pod] → Configurable; startup fails loudly on bind error.

## Migration Plan

Additive. Anyone probing `:9090/healthz` on the bridge must switch to `:10062/healthz`. Rollback: revert the commit.
