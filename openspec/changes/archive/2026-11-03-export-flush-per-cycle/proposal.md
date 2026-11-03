# Proposal

## Why

The OTLP `PeriodicReader` exports on its own timer, independent of the per-target collection cycles, so an export can carry state from a cycle still in progress or lag up to one interval behind the data. This violates the `metric-export` scenario "Metrics pushed after each collection cycle" ([#17](https://github.com/gjed/github-activity-exporter/issues/17)).

## What Changes

- After each target's collection cycle completes, the exporter forces an OTLP flush. With N targets this means N flushes per interval, each pushing all current snapshots. This is accepted.
- A flush failure is logged and counted in `github_exporter.otlp.errors`; the collection loop continues.
- The `PeriodicReader` stays as a fallback export path.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `metric-export`: "OTLP push as primary export" defines the per-target flush semantics and that a failed flush does not stop collection.

## Impact

- `internal/collector/runner.go`: new Runner option taking a flush function, called once per completed cycle.
- `cmd/github-activity-exporter/main.go`: wires `Provider.ForceFlush` into every Runner.
- `internal/collector/runner_test.go`: tests for one flush per cycle and non-fatal flush errors.
- No config, API, or dependency changes.
