# Tasks

## 1. Config

- [ ] 1.1 Add `health.listen_address` (default `:10062`), `health.stall_timeout` (default `0` = `2 × collection_interval + 1h`) and `health.unauthorized_cycles` (default 3) to `internal/config/config.go`; reject negative stall timeout, cycles below 1, and a listen address equal to the enabled bridge's; add config tests for defaults, custom values and each rejection; verify `go test ./internal/config/...`

## 2. Health tracker and handlers

- [ ] 2.1 Create `internal/health` with `Tracker` (per-target state, injectable clock) and `Handler()` for `/healthz` and `/readyz` per the spec; unit tests cover startup, readiness, discovery failure, default and custom stall timeout, exited loop, default and custom unauthorized threshold, isolated 401, recovery and 503 bodies; verify `go test ./internal/health/...`

## 3. Signal sources

- [ ] 3.1 Add `WithAuthResultHook` to `internal/github` (transport reports every response; installation-token 401 wraps `ErrUnauthorized` and reports through the token source); httptest tests for PAT 401/200 and App token 401; verify `go test ./internal/github/...`
- [ ] 3.2 Add an optional cycle observer to `collector.Runner` (`CycleStarted`, `CycleFinished(discoveryOK)`); runner tests for success and discovery failure; verify `go test ./internal/collector/...`

## 4. Wiring

- [ ] 4.1 In `main.go`, start the health server before the credential pings, register targets, mark credentials checked, wire hooks and observers, report early loop exit, shut the server down on exit; remove `/healthz` from the bridge in `internal/exporter/provider.go`; verify `go build ./...`

## 5. Documentation

- [ ] 5.1 Document `health.listen_address` in the README config block and add a health endpoints section with the exact criteria; verify markdown lint passes on `README.md`

## 6. Integration

- [ ] 6.1 Run `go build ./... && go test ./...`; verify both pass

## Workflow follow-up

- Archive the change with `openspec archive exporter-health-endpoints` before the final commit.
