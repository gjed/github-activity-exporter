# Proposal

## Why

The exporter logs JSON to stdout only. Those lines reach Loki through whatever log collector the deployment runs, and nothing ties them to the cycle traces added in [#75](https://github.com/gjed/github-activity-exporter/issues/75). OTLP logs carrying trace context let you go from a slow or failed cycle's trace straight to its log lines ([#76](https://github.com/gjed/github-activity-exporter/issues/76)).

## What Changes

- With `telemetry.enabled` and `telemetry.logs.enabled` ([#73](https://github.com/gjed/github-activity-exporter/issues/73)), every record the exporter logs also goes to the existing self-telemetry LoggerProvider through the `otelslog` bridge, exported over OTLP. The JSON log on stdout stays in every mode. With the logs signal off there is no bridge, no LoggerProvider, no exporter and no connection, and stdout is byte-for-byte what it would be without this change apart from the key renames below.
- Trace correlation: a record logged inside a span carries that span's `trace_id`/`span_id`, as the OTLP record's trace context and as `trace_id`/`span_id` keys on stdout. The stdout keys are added only while the logs signal is active, so stdout with logs off is unchanged. Logging calls inside spans pass the span context, including the per-repo and per-batch failures and the GitHub client's rate-limit logs reached from `Limiter` workers.
- Severity per the OTel log data model: `slog` Debug/Info/Warn/Error map to SeverityNumber 5/9/13/17. The OTLP minimum level is the stdout level, today a fixed `info`.
- Log attribute keys reuse registry attribute names. Renamed on stdout and OTLP:
  - `target` -> `exporter.target`
  - `repo` (`owner/name`) -> `github.org` + `github.repo`
  - `owner` -> `github.org`
  - `resource` (rate-limit pool) -> `github.api.rate_limit.resource`
- Credentials (PATs, the GitHub App private key and installation tokens, OTLP headers from `export.otlp.headers` and `headers_env`) never appear in either output; a test plants them and greps both.
- Errors of the OTLP exporters themselves (OTel SDK error handler, product OTLP push failures) stay on stdout only, so a down backend cannot feed log exports back into themselves; a test covers it.
- README "Self-telemetry": a logs section. Helm chart: the self-telemetry values example enables logs. No template change.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `self-telemetry`: OTLP logs bridged from the exporter log, trace correlation, severity mapping, registry attribute keys, no credentials in logs, no OTLP export errors in OTLP logs.

## Impact

- `cmd/github-activity-exporter/main.go`: stdout-only logger for everything built before self-telemetry, fan-out logger (stdout + OTLP) for the runners and the rest when logs are active.
- `internal/telemetry`: the bridge handler (level gate, `otelslog`) and the stdout trace-context handler.
- Call sites switched to the context-taking `slog` methods and/or renamed keys: `internal/collector/{runner,pr,issue,dashboard,batch}.go`, `internal/github/{client,permissions}.go`, `internal/discovery/discoverer.go`, `cmd/github-activity-exporter/main.go`.
- New dependency `go.opentelemetry.io/contrib/bridges/otelslog`.
- Tests: bridge on/off, correlation inside a `Limiter` worker, severity, credential redaction on both outputs, no feedback loop.
- `README.md`, `charts/github-activity-exporter/examples/self-telemetry-values.yaml`.
- Anyone parsing stdout JSON by the renamed keys must update their parsing. No change to metrics, spans or the registry.
