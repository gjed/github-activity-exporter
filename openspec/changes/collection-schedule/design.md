# Design

## Context

`Runner.Run` (`internal/collector/runner.go`) calls `runOnce` and then waits `time.After(r.interval)`, so the period is cycle duration + interval. `Discoverer` (`internal/discovery/discoverer.go`) caches the repo list after the first successful `refresh`; only `Start`, never called outside tests, refreshes it again. `Start` itself begins with a full `refresh`. The health tracker (`internal/health`) fails `/healthz` when no cycle completed within `stall_timeout` of the previous completion or of the loop start; `config.Load` defaults `stall_timeout` to `2 × collection_interval + 1h`.

## Goals / Non-Goals

**Goals:**

- One cycle start per `collection_interval` per target, never overlapping, no catch-up burst after an overrun.
- Overruns visible as a metric operators can alert on.
- Discovery refresh running in production for the runner's lifetime, keeping the last good list on failure.

**Non-Goals:**

- Changing the health tracker's stall logic or adding config keys.
- Aligning cycle starts to wall-clock boundaries or across targets.
- Triggering an extra collection cycle when a refresh finds new repos.
- A dashboard panel for overruns (the template is hand-curated; the counter is queryable without one).

## Decisions

### Fixed period from the previous start

`Run` records `start := time.Now()` before each cycle and, after it, waits `interval - time.Since(start)` on a timer stopped on exit. A wait of zero or less is an overrun: the next cycle starts at once (after a `ctx.Done()` check), so the next period is measured from that late start. Measuring from the actual start rather than from scheduled ticks is what prevents a catch-up burst; it also means drift after an overrun is not recovered, which is the intended behaviour. `time.Since` uses the monotonic clock, so wall-clock jumps do not shift the schedule. Rejected: `time.Ticker`. It drops ticks while a cycle runs but delivers one buffered tick right after an overrun and keeps the original phase, which is harder to state and test than a timer from the start.

### Overruns: warning log and `github_exporter.scrape.overruns`

Each overrun logs a warning with the target, the cycle duration and `collection_interval`, and increments `github_exporter.scrape.overruns` (counter, unit `{cycle}`, attribute `exporter.target`, `annotations.prometheus` name and type `counter`), recorded through a new `SelfMetricsRecorder.RecordOverrun`. A counter rather than a log alone because operators size `collection_interval` from it and alert on `increase(...) > 0`. `github_exporter.scrape.duration` does not answer the question: its bucket boundaries (…, 250, 500, …) do not line up with the configured interval, and the interval is not exported. The `scrape` prefix matches the existing cycle-duration metric. Overruns are not recorded once the context is cancelled.

### Stall timeout: default unchanged, re-derived

Let cycle k start at `S_k` and take `d_k`; it completes at `C_k = S_k + d_k`, and with the fixed period `S_{k+1} = S_k + max(I, d_k)`. The gap between completions is

`C_{k+1} - C_k = max(I, d_k) - d_k + d_{k+1} ≤ I + d_{k+1}`

because `max(I, d_k) - d_k ≤ I`. The bound is tight (a near-instant cycle followed by a long one) and equals the old semantics' exact gap, `I + d_{k+1}`. Before the first completion the gap from loop start is `d_1`. So a healthy target always completes within `I + d` of the previous completion, where `d` is the duration of the cycle in progress, and the default `stall_timeout = 2 × I + 1h` tolerates any single cycle up to `I + 1h`: one full period of work plus a full one-hour GitHub rate-limit pause. The formula and the tracker's completion-based check stay as they are; only the docs change to state the derivation. The `stall_timeout > collection_interval` validation still holds.

### Production refresh in `buildTargetRunner`

`buildTargetRunner` runs `go disc.Start(ctx)` with the ctx it receives, which is the context `main` passes to `Runner.Run`, so the refresh lives exactly as long as the collection loops and stops on SIGTERM. `Start` drops its initial `refresh`: the runner's first `Repos()` call already does the initial discovery, and keeping both would list every org twice at startup. `Start` now only ticks every `refresh_interval`. If the initial discovery failed, `Repos()` keeps retrying each cycle as today, and a tick may also refresh; the two can overlap once, which is harmless (`refresh` replaces the cache under its lock; owner-type lookups are guarded by `ownerMu`).

Rejected: refreshing lazily from `Repos()` when `refresh_interval` has elapsed. It puts the org listing inside the cycle (longer cycles, more overruns) and only refreshes as often as cycles run.

Rejected: a generic background-task option on `Runner` to tie the goroutine to `Run`'s own lifetime. `Run` only returns early on a bug that already fails `/healthz`; a discoverer still ticking then costs one listing per hour. One line in `buildTargetRunner` is enough.

### Failed refresh

`refresh` already returns before touching the cache on error, so the last good list stays. `Start` logs the error (as today) and calls an optional hook, `discovery.WithRefreshFailed(func())`; `buildTargetRunner` wires it to `selfMetrics.RecordAPIError(target, rest.repos)`, the same counter and endpoint the runner uses for a failed discovery inside a cycle. No new metric.

### Removed repos

Already handled once the list changes: each cycle collects only the repos `Repos()` returns, replaces the snapshots the OTel callback reports, and drops missing repos from `lastCollectedAt`. A test pins it.

### Tests on a fake clock

`testing/synctest` (Go 1.25+, the module is on 1.26) runs the runner and discoverer inside a bubble with virtual time, so `time.Now`, timers and tickers advance only when every goroutine is blocked. No clock interface is added to production code. Tests use in-process fakes only: a `RepoSource` whose `Repos` blocks on a channel or sleeps a virtual duration, and for the discoverer a `github.Client` whose `http.Client` transport calls the fake API handler in-process (a real loopback `httptest.Server` keeps goroutines blocked on network I/O, which synctest does not count as idle, so virtual time would never advance).

## Risks / Trade-offs

- \[Operators who sized `collection_interval` as a pause now get more frequent cycles and more API calls\] → For cycles shorter than the interval the rate goes from one per `d + I` to one per `I`; the rate-limit pause still caps API use. Called out in the README and the release notes via the `fix` commit.
- [Back-to-back cycles when cycles always overrun] → That is the configured freshness being unreachable; the overrun counter and warning make it visible so the operator raises `collection_interval`.
- \[Refresh and first `Repos()` overlap after a failed initial discovery\] → At most one duplicate listing; the cache write is locked.
