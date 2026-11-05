# Tasks

## 1. Registry and generated code

- [x] 1.1 Add `github_exporter.api.rate_limit.remaining` (`{request}`) and `github_exporter.api.rate_limit.reset` (`s`) gauges with `exporter.target` required to `registry/signals.yaml`; verify `make check-schema` exits 0
- [x] 1.2 Add `replace("Ratelimit", "RateLimit")` to `templates/go/metrics.go.j2` and run `make generate`; verify the diff is the two constants and list entries in `internal/semconv/metrics.go` plus two panels in `dashboards/github-activity.json`, and `go build ./...` passes

## 2. Self-metrics and wiring

- [x] 2.1 Add the two gauges and `RecordRateLimit(ctx, target, remaining, resetAt)` to `internal/exporter/selfmetrics.go`; verify `go build ./...`
- [x] 2.2 Register `ghclient.WithRateLimitHook` in `buildTargetRunner` (`cmd/github-activity-exporter/main.go`) recording both gauges for the target name; verify `go build ./... && go vet ./cmd/...`

## 3. Live-check

- [x] 3.1 Record both gauges via `RecordRateLimit` in `emitConformingMetrics` (`tests/integration/live_check_test.go`); verify `go test -tags integration -count=1 ./tests/integration/...` passes

## 4. Integration

- [x] 4.1 Run `go build ./... && go test ./... && make check-schema && make generate` and verify `git status` shows no unexpected diff
- [x] 4.2 Archive the change with `openspec archive github-rate-limit-gauges` and verify `openspec/specs/github-auth` and `openspec/specs/weaver-schema` contain the new requirements
