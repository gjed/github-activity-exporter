# Tasks

## 1. Bridge

- [x] 1.1 Add `go.opentelemetry.io/contrib/bridges/otelslog`
- [x] 1.2 `Telemetry.Logger(handler, level)`: same handler when logs are inactive; stdout handler with `trace_id`/`span_id` plus level-gated `otelslog` handler on a `slog.MultiHandler` when active
- [x] 1.3 Unit tests: inactive output byte-identical, severity 5/9/13/17 and text, level gate, trace context on both outputs, none outside a span

## 2. Wiring

- [x] 2.1 `main.go`: stdout-only logger for the health server, `exporter.New` and `telemetry.New`; `tel.Logger` for runners and later lines

## 3. Call sites

- [x] 3.1 Rename keys: `target`, `repo`, `owner`, `resource` to registry names in `internal/collector`, `internal/github`, `internal/discovery`, `main.go`
- [x] 3.2 Context-taking `slog` methods with the span context: runner cycle and collector lines, per-unit failures with the unit's repo or batch context (`pr.go`, `issue.go`, `dashboard.go`, `batch.go`), GitHub client rate-limit and cost lines, discovery refresh lines
- [x] 3.3 Unit tests: failed repo record carries its repo span at concurrency > 1; rate-limit record from a `Limiter` worker carries the unit's trace; renamed keys

## 4. End-to-end tests

- [x] 4.1 Mock GitHub: `GET /app` and installation token exchange
- [x] 4.2 Credential redaction: planted PAT, App key, installation token, `export.otlp.headers` and `headers_env` values absent from stdout and `/v1/logs` bodies
- [x] 4.3 No feedback loop: rejected log and metric exports logged on stdout, absent from later OTLP log records

## 5. Docs

- [x] 5.1 README "Self-telemetry": logs section (bridge, correlation, severity, level, keys, what stays on stdout)
- [x] 5.2 Helm self-telemetry values example enables logs

## 6. Validation

- [x] 6.1 `go build ./...`, `go test -race ./...`, `make check-schema`, `make generate` without diff, OTLP and `/metrics` live-checks
- [x] 6.2 One cycle against the mock GitHub, traces and logs on, to `grafana/otel-lgtm` (Collector, Loki, Tempo) in Docker: a log record from a `Limiter` worker with its span's `trace_id`/`span_id`
- [x] 6.3 Logs off: stdout JSON of main and of this branch against the same mock, `time` normalized and main's keys renamed, byte-identical
- [x] 6.4 Archive the change
