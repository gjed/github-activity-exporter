# Proposal

## Why

The exporter collects general GitHub PR and issue activity; Renovate's Dependency Dashboard is one data source among several. The name `renovate-exporter` (and `renovate-github-exporter` as the OTel service name) misrepresents that scope. This change renames the project to `github-activity-exporter` with no behavior change, so later changes (optional Dependency Dashboard collector, author/branch PR filters) land on the new identity.

## What Changes

- Go module path `github.com/gjed/renovate-exporter` -> `github.com/gjed/github-activity-exporter`, every import updated. The meter instrumentation scope names follow the module path.
- `cmd/renovate-exporter/` -> `cmd/github-activity-exporter/`; binary name in `Makefile`, `Dockerfile`, `.gitignore`, `--version` output and the startup log line.
- Image `ghcr.io/gjed/renovate-exporter` -> `ghcr.io/gjed/github-activity-exporter`: `image.yml` names the image explicitly instead of deriving it from `github.repository`, so the name is right before and after the repo rename.
- Default config path in the image `/etc/renovate-exporter/config.yaml` -> `/etc/github-activity-exporter/config.yaml`.
- OTel resource `service.name` `renovate-github-exporter` -> `github-activity-exporter` (`internal/exporter/provider.go`, `.weaver.toml` live-check matcher).
- Registry: manifest `name`, `description` and `schema_url`; resource attribute group `renovate_github_exporter.resource` -> `github_activity_exporter.resource` (plus the `templates/go/weaver.yaml` filter and `.weaver.toml`); attribute examples that use the old repo name.
- Dashboard: title `Renovate GitHub Exporter` -> `GitHub Activity Exporter`, uid `renovate-github-exporter` -> `github-activity-exporter`, file `dashboards/renovate-github.json` -> `dashboards/github-activity.json`, all via the template and `make generate`.
- `package.json` name/description, README (title, badges, image, install, config paths, one-line description), `AGENTS.md` title, `internal/semconv/doc.go`, `openspec/specs/**`.
- Unchanged: the `renovate.*` attribute namespace, `renovate.dashboard.*`, the Dependency Dashboard collector, `RENOVATE_BOT_LOGIN`, `renovate.json`/`.renovaterc.json` and `# renovate:` comments, `openspec/changes/archive/**`, the GitHub repository name, existing GHCR packages.

Not marked **BREAKING** (project stays on 0.x), but operators see these externally visible differences: new image name, new default config mount path, new `service.name` (so a new `job` label and a new series set in the backend), new dashboard uid.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `ci-release`: the image is published to `ghcr.io/gjed/github-activity-exporter`.
- `weaver-schema`: the resource attribute group is `github_activity_exporter.resource` with `service.name` `github-activity-exporter`; the dashboard is generated to `dashboards/github-activity.json`.

## Impact

- Code: every Go file importing internal packages, `cmd/`, `internal/exporter/provider.go`.
- Generated: `internal/semconv/*.go` (example strings in comments), dashboard JSON (renamed file, title, uid).
- CI/release: `.github/workflows/image.yml`, `Dockerfile`, `Makefile`, `.golangci.yml` (local-prefix import grouping).
- Users: pull the new image, mount config at the new path, update dashboards/alerts filtering on `job="renovate-github-exporter"`. Old `ghcr.io/gjed/renovate-exporter` images stay published as-is.
