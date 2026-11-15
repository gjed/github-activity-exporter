# Tasks

## 1. Dependency

- [x] 1.1 Remove the `github.com/prometheus/common` packageRule from `renovate.json`
- [x] 1.2 Bump `github.com/prometheus/common` to v0.74.0 and run `go mod tidy`

## 2. Tests and docs

- [x] 2.1 Scrape the `openmetrics` live-check case with the Prometheus v3.16.0 default `Accept` header
- [x] 2.2 Document the OpenMetrics-only scrape in `docs/metrics.md`

## 3. Verification

- [x] 3.1 `go build ./...`, `go test ./...`, `make check-schema`, `make generate` with no diff
- [x] 3.2 Integration tests with weaver 0.28.0 and `make test-e2e`
- [x] 3.3 `curl` `/metrics` with text 1.0.0, OpenMetrics-only, no `Accept` and text 0.0.4 headers
