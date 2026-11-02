# Design

## Context

Pure rename of `renovate-exporter` to `github-activity-exporter`. The GitHub repository is renamed by the maintainer after merge (`gh repo rename`); GitHub then redirects the old URL. Until then the module path does not resolve remotely, which only affects `go install` from outside, not builds from a checkout.

## Goals / Non-Goals

**Goals:** every project-identity string uses the new name; generated files change only through the registry/templates and `make generate`.

**Non-Goals:** renaming Renovate-specific identifiers (`renovate.*` attributes, Dependency Dashboard collector, `RENOVATE_BOT_LOGIN`, Renovate config files), the archive, the GitHub repo, existing GHCR packages; making the Dependency Dashboard collector optional; new PR filters.

## Decisions

- **Hardcode the image name in `image.yml`.** `ghcr.io/${{ github.repository }}` resolves to the old name until the repo is renamed. A literal `ghcr.io/gjed/github-activity-exporter` is correct in both states. Alternative (keep `github.repository`) would publish a release cut between merge and repo rename to the old package.
- **Rename the default config path** to `/etc/github-activity-exporter/config.yaml`. It ships only in the new image, so nobody running the old image is affected; keeping the old path would leave a stale name in every deployment manifest.
- **Rename the dashboard file** to `dashboards/github-activity.json` via `templates/grafana/weaver.yaml`, together with title and uid. The old file is deleted by the regeneration (`git mv` is not used: the file is generated).
- **Rename the resource attribute group id** to `github_activity_exporter.resource`; it names the project, not Renovate. `.weaver.toml` and the `templates/go/weaver.yaml` filter follow.
- **Instrumentation scope names** follow the module path (`github.com/gjed/github-activity-exporter[/<target>]`). Changes the `otel_scope_name` label; accepted as part of the rename.
- **README one-liner**: describe the project as a GitHub PR/issue activity exporter with Renovate Dependency Dashboard support, instead of "exporter for Renovate". Nothing else in the README is rewritten.

## Risks / Trade-offs

- New `service.name` → new `job` label → series discontinuity in the backend → noted in the PR description; dashboards use `$job`, so they keep working after selecting the new value.
- Release: the env-prefix rename is typed `fix`, so the merge cuts a 0.x patch release and `go install …@latest` and the new image resolve on day one.

## Migration Plan

Merge → maintainer runs `gh repo rename github-activity-exporter` and updates the local remote → next release publishes `ghcr.io/gjed/github-activity-exporter`. Rollback: revert the merge; nothing outside the repo changes.
