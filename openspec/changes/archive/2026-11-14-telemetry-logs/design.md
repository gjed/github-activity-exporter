# Design

## Context

`cmd/github-activity-exporter/main.go` builds one `slog.JSONHandler` on stdout at a fixed `info` level and hands that logger to every component. [#73](https://github.com/gjed/github-activity-exporter/issues/73) already builds an SDK `LoggerProvider` (batch processor, OTLP exporter on the `export.otlp` or `OTEL_EXPORTER_OTLP_*` destination) when the logs signal is active, but nothing writes to it. [#75](https://github.com/gjed/github-activity-exporter/issues/75) added the cycle, collector, repo, batch, flush and rate-limit wait spans; repo and batch units run in `Limiter.each` worker goroutines, and per-unit failures are logged after the workers return, in repo order, without the unit's span context.

## Goals / Non-Goals

**Goals:** OTLP logs alongside stdout; trace correlation on both outputs; OTel severity; registry attribute keys; no credentials; no feedback loop from the exporters' own errors.

**Non-Goals:** a log-level setting (there is none today; the level stays `info`), log records in the Weaver registry, new log lines, changes to metrics or spans, stdout format changes other than the renames and the `trace_id`/`span_id` keys while logs are active.

## Decisions

### 1. Two loggers, chosen by construction order

`main` builds the stdout JSON handler as today and a stdout-only logger from it, which stays `slog.Default()`. Everything built before self-telemetry gets that logger: the health server, `exporter.New` (product OTLP push failures in `error_counting.go`, the Prometheus bridge) and `telemetry.New` (OTel SDK error handler, profiles). After `telemetry.New`, `tel.Logger(handler, level)` returns the logger for everything else (runners, collectors, GitHub client, discovery, startup and shutdown lines):

- logs inactive: `slog.New(handler)`, the very same handler, so stdout is unchanged byte for byte;
- logs active: `slog.NewMultiHandler(stdout, otlp)` (Go 1.26 standard library), where `stdout` wraps the JSON handler and adds `trace_id`/`span_id`, and `otlp` is `otelslog.NewHandler(ScopeName, WithLoggerProvider(lp), WithVersion(version))` behind a level gate with the stdout `Leveler`. The gate is needed because the SDK logger reports every level enabled.

This keeps the OTLP exporters' own errors on stdout by construction: no runtime filter, nothing to forget. The SDK error handler covers the trace, log and metric exporters of self-telemetry; `error_counting.go` covers the product push. A runner's `metrics flush failed` warning is a runner log line, not an exporter error, and stays bridged; it cannot loop, because a failed log export goes to the SDK error handler, which is stdout-only.

Alternative rejected: a filter on message text or logger scope in the bridge. Fragile and invisible.

### 2. Trace context on stdout

The stdout wrapper adds `trace_id` and `span_id` (lower-case hex, W3C form) to a record whose context holds a valid span context, after the record's own attributes. No component uses `WithGroup`, so the keys stay top level. It is installed only while the logs signal is active, so the traces-on/logs-off stdout is unchanged. OTLP records get their trace context from the context passed to `Handle`; `otelslog` does that already.

### 3. Severity

`otelslog` maps an `slog.Level` to `SeverityNumber` as level + 9 (Debug -4 -> 5, Info 0 -> 9, Warn 4 -> 13, Error 8 -> 17) and sets SeverityText to the level name, which is the OTel data model mapping. A unit test pins it.

### 4. Context at call sites

The record carries the context of the span the work belongs to:

- `internal/collector/runner.go`: cycle start/complete, repo discovery failure, collector failures use the cycle or collector span context.
- `internal/collector/pr.go`, `issue.go`, `dashboard.go`: per-unit failures stay in the merge loop after the workers, in repo order as today, logged with the context of the unit's repo or batch span. The work item stores the unit context; `eachBatch` returns each batch's context. Logging from inside the worker would reorder stdout lines under concurrency.
- `internal/github/client.go`: the rate-limit lines (`threshold reached`, `recovered`, `waiting for window reset`, `giving up`, `secondary rate limit`, missing reset, malformed remaining) and the GraphQL cost debug line use the request context. They run inside `Limiter` workers with the context the rate-limit transport receives, outside `otelhttp`, so they carry the repo or batch span. `updateRateLimit` and `recordRateLimit` take a context for this.
- `internal/discovery/discoverer.go`: the refresh lines use the context passed in, the cycle span when discovery runs inline in a cycle.

Startup lines (`permissions.go`, `main.go`) run outside any span; they get key renames only.

### 5. Attribute keys

`target` -> `exporter.target`, `repo` (`owner/name`) -> `github.org` + `github.repo`, `owner` -> `github.org`, `resource` -> `github.api.rate_limit.resource`, on every call site, in every mode. `err` stays: it is the error message, and no registry attribute holds a message. `error.type` and `github.api.endpoint` are registry attributes that no log line carries today; this change adds no attribute to existing lines. Other keys (`count`, `wait`, `status`, ...) have no registry counterpart and stay.

### 6. Credentials

No current log line takes a credential: auth errors name the target, `resolveHeadersEnv` errors carry no value. The guarantee is a test, not a filter: `tests/e2e` runs the built binary with logs and traces on against the mock GitHub with a PAT target and a GitHub App target (the mock gains `GET /app` and the installation token exchange), planted OTLP headers in `export.otlp.headers` and `headers_env`, and an in-process OTLP receiver; it asserts that no planted value (PAT, PEM key body, installation token, header values) occurs on stdout or in any `/v1/logs` request body. No Docker needed.

### 7. Feedback-loop test

Same harness: the receiver answers the first `/v1/logs` and `/v1/metrics` requests with 400 (not retried), then 200. The test asserts the export errors are on stdout and that no OTLP log record received afterwards carries an export error message.

## Risks / Trade-offs

- Renamed keys break stdout parsers keyed on `target`, `repo`, `owner` or `resource`. Listed in the PR body.
- Lines logged before `telemetry.New` and the exporter package's lines never reach OTLP, including the health and Prometheus server errors. Accepted: they are startup lines or export-path lines.
- The batch processor drops records when its queue is full while the backend is down; stdout still has every line.
