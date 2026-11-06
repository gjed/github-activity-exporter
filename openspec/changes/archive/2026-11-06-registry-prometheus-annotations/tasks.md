# Tasks

## 1. Contract in the registry

- [x] 1.1 Add `annotations.prometheus.{name,type}` to every metric in `registry/signals.yaml`; `make generate` produces no diff
- [x] 1.2 Add the `after_resolution` policy in `policies/registry/`; verify `weaver registry check --v2 -r registry/ -p policies/registry/` exits 0, and exits non-zero for a missing annotation, a wrong name and a wrong type (scratch copy of the registry, not committed)
- [x] 1.3 Run the policy from `make check-schema` (Docker and binary paths) and `.github/workflows/registry-validate.yml`; verify `make check-schema` passes
- [x] 1.4 Replace the prose contract in `registry/manifest.yaml` with a pointer to the annotations, the policy and `README.md`; point the comment in `internal/exporter/provider.go` at the annotations; verify `go build ./...`

## 2. Weaver verifies /metrics

- [x] 2.1 Add `policies/live_check/` (`prometheus_type_mismatch`) and `.weaver-prometheus.toml` (resource matcher, `unexpected_instrument`/`unit_mismatch` downgraded)
- [x] 2.2 Add the exposition-to-samples converter and a file-input live-check runner in `tests/integration`
- [x] 2.3 Add the `/metrics` live-check test (OpenMetrics-first and text `Accept`), and negative tests for `github.pr.automerged_total` and a `gauge`-typed `github.pr.automerged`; verify with `go test -tags integration ./tests/integration/...`
- [x] 2.4 Make the OTLP names test fail on any violation other than `required_attribute_not_present`; verify with the integration suite
- [x] 2.5 Remove `internal/exporter/naming_test.go`; verify `go test ./...`

## 3. Validation

- [x] 3.1 `go build ./...`, `go test ./...`, `make check-schema`, `make generate` with no diff, integration suite passing

## Workflow follow-up

- `openspec archive registry-prometheus-annotations` before the final commit, in the same PR.
