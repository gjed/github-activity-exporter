# Tasks

## 1. Fixtures

- [ ] 1.1 Store real dashboard bodies under `internal/collector/testdata/dashboards/` fetched with `gh issue view --json body`: gjed/github-configs-public#16, gjed/renovate-config#3, gjed/siemens-converter#19, gjed/sinapsi-converter#18, gjed/terraform-github-config-as-yaml#99, gjed/songbooks#100, zextras/carbonio-mails-ui#1064 (several populated sections), zextras/service-discover-template#10 (only "PR Edited (Blocked)"); verify each file is byte-identical to the fetched body

## 2. Parse error semantics (#78)

- [ ] 2.1 Detect a Renovate dashboard by the four markers in design.md, set `parse_error` only when none is present, and zero-fill the four tracked sections; verify with table-driven tests asserting `parse_error=0` and all-zero queues for the five no-branch fixtures and zextras/service-discover-template#10, and `parse_error=1` for a non-dashboard body
- [ ] 2.2 Update the `renovate.dashboard.parse_error` note in `registry/signals.yaml`, run `make generate`, and verify `make check-schema` passes

## 3. Branch-only queue counts (#79)

- [ ] 3.1 Count only branch-referencing checkboxes; verify with tests asserting exact per-section counts for gjed/songbooks#100 (rate-limited 11) and zextras/carbonio-mails-ui#1064, cross-checked against a `grep -c` of `-branch=` per section
- [ ] 3.2 Convert the synthetic bodies in `internal/collector/dashboard_test.go`, `tests/integration/collector_loop_test.go` and `tests/e2e/mockgithub_test.go` to Renovate markup (`- [ ] <!-- <type>-branch=<name> -->...`) so their existing expected counts hold; verify `go test ./...` passes
- [ ] 3.3 Update the `renovate.dashboard.queue` note in `registry/signals.yaml`, run `make generate`, and verify `make check-schema` passes

## 4. Integration

- [ ] 4.1 Run `go build ./...`, `go test ./...`, `make check-schema`, `make test-e2e`, and verify all pass

## Workflow follow-up

- Run `openspec archive renovate-dashboard-parsing` before the final commit, in the same PR.
- Open the PR with body "Closes #78, closes #79", assignee gjed.
