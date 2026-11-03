# Design

## Context

`config.Load` unmarshals the YAML with viper, then `resolveEnvVars` copies `auth.token_env` into `auth.token` and `auth.app.private_key_env` into `auth.app.private_key_value`, then `validateAuth` requires exactly one of PAT or App. Downstream code only reads the resolved `Auth`.

Viper is configured with `SetEnvPrefix("GITHUB_ACTIVITY_EXPORTER")` and `AutomaticEnv()`. Verified on `main` (992708b): `GITHUB_ACTIVITY_EXPORTER_EXPORT_OTLP_ENDPOINT` and `GITHUB_ACTIVITY_EXPORTER_COLLECTION_INTERVAL` override the file; `GITHUB_ACTIVITY_EXPORTER_TARGETS_0_AUTH_TOKEN` has no effect, because viper does not address elements of the `targets` list.

## Goals / Non-Goals

**Goals:**

- Empty or omitted `auth` resolves to `GITHUB_TOKEN`.
- Missing credential is a startup error naming the target.

**Non-Goals:**

- Making `GITHUB_ACTIVITY_EXPORTER_*` able to set per-target auth.
- Reading other token variables (`GH_TOKEN`, `GITHUB_PAT`).

## Decisions

- **Fallback trigger is a zero-value `Auth`.** `Auth` holds only scalars, so `t.Auth == (Auth{})` covers both `auth: {}` and an omitted `auth` key. Alternative: fall back whenever `validateAuth` would report "no method". Rejected: a half-written `app` block (e.g. `installation_id` without `app_id`) would silently switch to a PAT instead of reporting the typo.
- **Resolve in `resolveEnvVars`.** The fallback writes `auth.token`, same as `token_env` does, so `validateAuth` and everything downstream stay unchanged. Alternative: resolve in `main.go` or the auth package. Rejected: it would split credential resolution across two places and `validateAuth` would still reject the target.
- **Error text:** `target "<name>": no auth configured and GITHUB_TOKEN is not set; set auth.token, auth.token_env, auth.app, or GITHUB_TOKEN`.
- **Precedence, highest first:** explicit `auth` on the target (`token`, `token_env`, `app`); then `GITHUB_TOKEN`. `GITHUB_ACTIVITY_EXPORTER_*` overrides apply to scalar keys only and never to `targets[].auth`. `GITHUB_TOKEN` does not carry the prefix, so viper never picks it up as a key override.

## Risks / Trade-offs

- \[Several targets with empty `auth` share one `GITHUB_TOKEN`\] → Intended: they did not configure distinct credentials. The credential isolation requirement covers credentials configured per target and still holds. README states the fallback is shared.
- \[Developer shells often export `GITHUB_TOKEN`, so the existing "no auth is error" test would pass or fail depending on the environment\] → Tests set `GITHUB_TOKEN` explicitly, including to empty.
