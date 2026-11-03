# Design

## Context

`exporter.Provider.ForceFlush` exists but nothing calls it. Each target has its own `collector.Runner` goroutine. The runner already depends on a `SelfMetricsRecorder` interface (satisfied by `exporter.SelfMetrics`), not on the `exporter` package. OTLP export errors are already logged and counted by `errorCountingExporter`, which swallows them so the reader stays alive.

## Goals / Non-Goals

**Goals:**

- One flush per completed cycle, per target.
- A flush error never stops the runner loop.

**Non-Goals:**

- Coordinating flushes across targets (one flush per interval for all targets).
- Removing or retiming the `PeriodicReader`.

## Decisions

- **Injected flush func, not a Provider dependency.** Add `collector.WithFlush(func(context.Context) error)`. `main.go` passes `provider.ForceFlush`. This keeps `collector` free of an `exporter` import and makes the test a plain counting func. Alternative: a `Flusher` interface; rejected as more code for one method.
- **Flush at the end of `runOnce`.** Every cycle ends with a flush, including a cycle whose repo discovery failed, so the `github_exporter.api.errors` increment from that cycle is pushed too. `RunOnce` therefore flushes, which the tests use.
- **Skip the flush when the context is cancelled.** On shutdown `Provider.Shutdown` already does a final flush; flushing on a cancelled context would only fail and count a spurious error.
- **Count non-export flush errors via self-metrics.** OTLP export failures are counted by `errorCountingExporter` and never reach `ForceFlush`'s return value. Any other error `ForceFlush` returns (e.g. a callback error) is logged as a warning and counted with `SelfMetricsRecorder.RecordOTLPError(target)`. This adds `RecordOTLPError` to the interface; `exporter.SelfMetrics` already implements it. Context errors are logged but not counted: `PeriodicReader` serves flushes one at a time and starts each caller's timeout before it queues, so while one target's export hangs, other targets' flushes time out without ever being attempted. Counting them would grow the counter with the number of targets, not the number of failed pushes; the hung export itself is counted once by `errorCountingExporter`. An error returned after the context is cancelled (shutdown mid-flush) is ignored.

## Risks / Trade-offs

- [N targets → N exports per interval, each with all snapshots] → Accepted; payload is small and the backend deduplicates by timestamp/series.
- [PeriodicReader still exports on its own timer] → Kept deliberately as a fallback; it can still emit a mid-cycle snapshot, but every completed cycle is now pushed promptly.
- [Concurrent ForceFlush from several runners] → The SDK MeterProvider and PeriodicReader are safe for concurrent ForceFlush.
