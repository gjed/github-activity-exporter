# Design

## Context

`parseDashboard` (`internal/collector/dashboard.go`) treats a body with none of the four tracked `##` headings as a parse error, and counts every `- [ ]`/`- [x]` line in a tracked section.

The rules below come from Renovate's own dashboard writer, [`lib/workers/repository/dependency-dashboard.ts` at tag `44.222.4`](https://github.com/renovatebot/renovate/blob/44.222.4/lib/workers/repository/dependency-dashboard.ts) (blob `f213a304dde362ef62bad409d65a63f57b2bd7c3`), and the default header in [`lib/config/options/index.ts` at `44.222.4`](https://github.com/renovatebot/renovate/blob/44.222.4/lib/config/options/index.ts):

- Every branch list item is `getCheckbox(`${type}-branch=${branchName}`)`, rendered as `- [ ] <!-- <type>-branch=<branch> -->`. Renovate's own parser matches it with `<!-- ([a-zA-Z]+)-branch=([^\s]+) -->`. Types seen: `approve`, `approveGroup`, `unschedule`, `unlimit`, `retry`, `approvePr`, `rebase`, `unpend`, `other`, `recreate`.
- Bulk actions are `getCheckbox(<constant>)` without `-branch=`: `approve-all-pending-prs`, `create-all-awaiting-schedule-prs`, `create-all-rate-limited-prs`, `rebase-all-open-prs`. Other non-branch checkboxes: `create-config-migration-pr`, and the manual-run checkbox.
- The body starts with `dependencyDashboardHeader`, default `This issue lists Renovate updates and detected dependencies. ...`. The header is user-configurable, so it is not a reliable marker on its own.
- With no branches, Renovate writes `This repository currently has no open or pending branches.`
- `PackageFiles.getDashboardMarkdown` writes `## Detected Dependencies`.

## Goals / Non-Goals

**Goals:**

- `parse_error` distinguishes "not a Renovate dashboard" from "dashboard with nothing queued".
- Queue counts equal the number of branches Renovate lists in the section.

**Non-Goals:**

- Tracking more sections (Errored, PR Edited (Blocked), Pending Status Checks, and so on).
- Changing metric names, attributes, or units.

## Decisions

### Dashboard detection

A body is a Renovate dashboard when it contains at least one fixed Renovate marker:

1. the default header phrase `This issue lists Renovate updates`;
1. `This repository currently has no open or pending branches.`;
1. a `## Detected Dependencies` heading;
1. a branch checkbox comment `<!-- <type>-branch=<name> -->`.

Otherwise `parse_error=1` and no queue series are emitted, as today.

Alternative: the default header only, as the issue suggests. Rejected because `dependencyDashboardHeader` is configurable; a repo with a custom header would report a permanent false parse error. Markers 2–4 come from code paths that the header option does not touch. Any real dashboard has marker 2 (no branches) or marker 4 (at least one listed branch), and almost always marker 3.

### Zero-filled sections

A detected dashboard reports all four tracked sections; any section missing from the body reports 0. Without this, an up-to-date repo would emit `parse_error=0` and no queue series at all, which reads as "no data" rather than "empty queue" and breaks `sum by (section)` panels and absence-based alerts.

Alternative: emit only sections present in the body (current behavior). Rejected for the reason above.

### Branch-only checkbox counting

A line counts toward a section when it matches `^\s*- \[[ xX]\] <!-- [a-zA-Z]+-branch=\S+ -->`, which mirrors Renovate's own `generalBranchRe`. Lines under `###` category subheadings stay inside the enclosing `##` section, because Renovate nests categories under the section heading.

Alternative: denylist the bulk-action comment names. Rejected: any future non-branch checkbox would be overcounted again.

## Risks / Trade-offs

- [Renovate changes its markup] → `parse_error` would rise on markup changes that drop all four markers, which is what the metric exists to signal. Queue counts would drop to 0 if the branch comment format changes; the testdata fixtures pin the current format.
- [Zero-filled series are new] → dashboards and alerts gain series with value 0 for absent sections; no existing query breaks.
