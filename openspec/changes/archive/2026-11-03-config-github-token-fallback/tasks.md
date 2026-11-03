# Tasks

## 1. Config fallback

- [x] 1.1 In `resolveEnvVars` (`internal/config/config.go`), when a target's `Auth` is the zero value, set `auth.token` from `GITHUB_TOKEN`, or return the error naming the target if it is unset or empty; update the `Auth` doc comment; verify `go build ./...`
- [x] 1.2 Add table tests in `internal/config/config_test.go` (with an `errContains` check) and testdata for: `auth: {}` with `GITHUB_TOKEN` set; omitted `auth` with `GITHUB_TOKEN` set; `auth: {}` with `GITHUB_TOKEN` empty (error names the target); explicit `auth.token` and `auth.app` with `GITHUB_TOKEN` set (explicit wins); partial `app` block with `GITHUB_TOKEN` set (still errors); verify `go test ./internal/config/...`

## 2. Documentation

- [x] 2.1 Update `README.md`: quick start config without `auth`, config reference comment on the fallback, `GITHUB_TOKEN` row in the env var table, and a precedence note covering explicit `auth` > `GITHUB_TOKEN` and the `GITHUB_ACTIVITY_EXPORTER_` prefix not reaching per-target `auth`; verify markdown lint passes on `README.md`

## 3. Integration

- [x] 3.1 Run `go build ./... && go test ./...`; verify both pass
