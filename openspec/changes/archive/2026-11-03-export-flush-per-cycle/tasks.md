# Tasks

## 1. Runner flush

- [x] 1.1 Add `WithFlush(func(context.Context) error)` to `internal/collector/runner.go` and call it once at the end of every `runOnce` (skipped when the context is cancelled); verify with `go build ./...`
- [x] 1.2 Log a flush error as a warning and count it via `SelfMetricsRecorder.RecordOTLPError`; verify the loop continues in the runner test
- [x] 1.3 Add runner tests: one completed cycle triggers exactly one flush; a flush error is logged, counted, and the next cycle still runs and flushes; verify with `go test ./internal/collector/...`
- [x] 1.4 Update the stale comments in `runner.go` (callback "on each PeriodicReader flush") and `provider.go` (`ForceFlush` "useful in tests"); verify by reading the diff

## 2. Wiring

- [x] 2.1 Pass `collector.WithFlush(provider.ForceFlush)` to every Runner in `cmd/github-activity-exporter/main.go`; verify with `go build ./... && go test ./...`

## 3. Archive

- [x] 3.1 Run `openspec archive export-flush-per-cycle` and verify `openspec/specs/metric-export/spec.md` contains the new scenarios
