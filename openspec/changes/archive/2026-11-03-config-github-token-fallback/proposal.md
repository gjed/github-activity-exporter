# Proposal

## Why

`GITHUB_TOKEN` is the token variable `gh`, Actions and most exporters use, but nothing in the exporter reads it. A target without `auth.token`, `auth.token_env` or `auth.app` is rejected, so the only way to use `GITHUB_TOKEN` is to write `token_env: GITHUB_TOKEN` on every target. [Issue #21](https://github.com/gjed/github-activity-exporter/issues/21).

## What Changes

- A target whose `auth` block is empty or omitted uses the `GITHUB_TOKEN` environment variable as a PAT.
- If such a target finds `GITHUB_TOKEN` unset or empty, startup fails with an error that names the target and lists the ways to supply a credential.
- An explicit `auth.token`, `auth.token_env` or `auth.app` always wins; `GITHUB_TOKEN` is never consulted for those targets.
- A partially filled `auth` block (e.g. `app` without `app_id`) keeps today's validation error; it does not fall back.
- Document the precedence, including that the `GITHUB_ACTIVITY_EXPORTER_` env override prefix only reaches scalar keys and cannot set per-target `auth`.
- Update the README: the quick start config drops the `auth` block, the config reference notes the fallback, the env var table gains `GITHUB_TOKEN`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `github-auth`: the PAT authentication requirement gains the `GITHUB_TOKEN` fallback, the missing-credential error, and the explicit-auth-wins scenario.

## Impact

- `internal/config/config.go`: fallback in env resolution; `validateAuth` unchanged.
- `internal/config/config_test.go` and `testdata/`: new cases for fallback, missing token, omitted `auth`, explicit auth precedence.
- `README.md`: quick start, config reference, env var table. The repo has no standalone example config file; the README blocks are the example config.
- No change to `cmd/` or the GitHub client: the fallback resolves into `auth.token` before anything else reads it.
- Out of scope: GitHub base URL (#22), any change to the `GITHUB_ACTIVITY_EXPORTER_` prefix behaviour.
