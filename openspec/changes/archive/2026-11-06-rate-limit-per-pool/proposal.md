# Proposal

## Why

`rateLimitTransport` keeps one `RateLimitState` per client, overwritten by every response, but GitHub counts REST, GraphQL and search against separate pools named by the `X-RateLimit-Resource` header ([#59](https://github.com/gjed/github-activity-exporter/issues/59)). The pause (`remaining < 200`) acts on whichever pool answered last, so a low GraphQL pool pauses REST calls and a low pool can be missed. `github_exporter.api.rate_limit.remaining` and `.reset` flip between pools.

## What Changes

- GitHub client (`internal/github/client.go`): one rate limit state and pause flag per pool. The pool a request will use is classified from its URL before it is sent, and the request waits only if that pool is paused. The response's `X-RateLimit-Resource` header names the pool the state is recorded under; a missing header falls back to the classified pool, a value outside the documented pools is recorded as `other`.
- `RateLimitState` gains the pool name, so `RateLimitHook` reports which pool changed; the state accessor takes a pool name.
- Registry (`registry/attributes.yaml`): new enum attribute `github.api.rate_limit.resource`, closed to the pools GitHub documents plus `other`. `registry/signals.yaml` adds it as required to `github_exporter.api.rate_limit.remaining` and `.reset`. `annotations.prometheus` is unchanged.
- Templates: `templates/go/attributes.go.j2` gains the casing `replace()` calls the new key and members need. `make generate` regenerates `internal/semconv/attributes.go` (and `dashboards/github-activity.json` if the template output changes).
- Self-metrics (`internal/exporter/selfmetrics.go`) and wiring (`cmd/github-activity-exporter/main.go`): both gauges record the pool attribute from the hook.
- The 401 auth-result hook that feeds `/healthz` ([#18](https://github.com/gjed/github-activity-exporter/issues/18)) is unchanged.
- Tests: a low GraphQL pool does not pause REST and vice versa; missing and unknown header handling; the gauges keep one series per pool; the live-check test records the gauges with the new attribute.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `github-auth`: rate limit awareness is tracked and enforced per pool; the gauges carry the pool.
- `weaver-schema`: the rate limit gauges declare `github.api.rate_limit.resource` as required.

## Impact

- Code: `internal/github/client.go`, `internal/exporter/selfmetrics.go`, `cmd/github-activity-exporter/main.go`, and their tests; `tests/integration/live_check_test.go`.
- Registry/templates: `registry/attributes.yaml`, `registry/signals.yaml`, `templates/go/attributes.go.j2`; regenerated `internal/semconv/`.
- Metrics: both gauges gain a label, so existing series are replaced by per-pool series (`core`, `graphql` in practice). Queries that do not aggregate on the new label return one series per pool instead of one per target.
- Specs: `openspec/specs/github-auth`, `openspec/specs/weaver-schema`.
