# Proposal

## Why

The Renovate Dependency Dashboard collector reports wrong values on real dashboards. `renovate.dashboard.parse_error` is 1 on every up-to-date repo, because a dashboard with no tracked sections counts as unparseable ([#78](https://github.com/gjed/github-activity-exporter/issues/78)). `renovate.dashboard.queue` overcounts every section that carries a Renovate bulk-action checkbox by one ([#79](https://github.com/gjed/github-activity-exporter/issues/79)).

## What Changes

- `renovate.dashboard.parse_error` is 1 only when the issue body is not a Renovate Dependency Dashboard. A dashboard with no open or pending branches, or with only sections the exporter does not track, reports 0.
- Every parsed dashboard reports all four tracked sections (`awaiting-schedule`, `rate-limited`, `pending-approval`, `pending`); a section absent from the body reports 0.
- `renovate.dashboard.queue` counts only checkboxes that reference a branch (`<!-- <type>-branch=<name> -->`). Bulk-action checkboxes (`approve-all-pending-prs`, `create-all-awaiting-schedule-prs`, `create-all-rate-limited-prs`, `rebase-all-open-prs`) and any other non-branch checkbox are excluded.
- Registry notes for both metrics describe the new semantics.
- Unit tests use real dashboard bodies stored as testdata.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `pr-collection`: adds requirements for when a dashboard counts as a parse error and for which checkboxes count toward a section's queue.

## Impact

- `internal/collector/dashboard.go`: dashboard detection and checkbox matching.
- `internal/collector/dashboard_test.go`, new `internal/collector/testdata/dashboards/*.md`.
- `registry/signals.yaml` notes; regenerated `internal/semconv` and `dashboards/` output if the notes flow into them.
- Alerts on `renovate.dashboard.parse_error > 0` stop firing on healthy repos. Queue values for sections with a bulk action drop by one. New zero-valued queue series appear for absent tracked sections.
