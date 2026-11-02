## Context

Weaver 0.28 is pinned in the Makefile and both workflows; the integration test still pins 0.23.0. The registry uses `file_format: definition/2`. Findings below come from running weaver 0.28.0 against this registry and from the v0.28.0 source/docs.

## Goals / Non-Goals

**Goals:** registry declares every metric attribute and resource attribute with requirement levels; `weaver registry check` emits no `requirement_level` warnings; live-check fails on anything the registry does not declare.

**Non-Goals:** naming/suffix policy (#14), rate-limit gauges (#16), migrating `provider.go` to generated resource constants.

## Decisions

### Manifest filename: keep `manifest.yaml`

Weaver CHANGELOG (v0.21 era, PR #1202): "Default to `manifest.yaml` for registry manifest file, deprecate `registry_manifest.yaml` and add warning when it's used." `weaver registry check` on 0.28 prints `Found registry manifest: /workspace/registry/manifest.yaml`. Live-check on 0.28 warns that the manifest is missing `file_format` and asks for `file_format: definition_manifest/2.0` (const in `schemas/definition-manifest.v2.json`). So the repo is right; the spec wording is fixed and `file_format` is added.

### Attribute requirement levels per metric

| Metric                                                                                                                    | Attributes                                                                                                                                                               |
| ------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `github.pr.count`                                                                                                         | target, org, repo required; `github.pr.state` required; `github.pr.label` conditionally required (label breakdown points)                                                |
| `github.pr.age`, `github.pr.close.duration`, `github.pr.automerged`, `github.issue.age`, `renovate.dashboard.parse_error` | target, org, repo required                                                                                                                                               |
| `github.pr.review_status`                                                                                                 | base + `github.pr.review_status` required                                                                                                                                |
| `github.issue.count`                                                                                                      | base + `github.issue.state` conditionally required (state points) + `github.issue.label` conditionally required (label points); `issue.go` emits them on separate points |
| `renovate.dashboard.queue`                                                                                                | base + `renovate.dashboard.section` required                                                                                                                             |
| `github_exporter.scrape.duration`, `github_exporter.otlp.errors`                                                          | `exporter.target` required                                                                                                                                               |
| `github_exporter.api.errors`                                                                                              | `exporter.target`, `github.api.endpoint` required                                                                                                                        |

Signal `requirement_level` is `recommended` on every metric: weaver 0.28 only allows `recommended` or `opt_in`, and all metrics are emitted by default.

### `endpoint` attribute

Key `github.api.endpoint`, closed enum: `graphql` (PR collector, GraphQL `pullRequests` query), `rest.issues` (issue and dashboard collectors, both `Issues.ListByRepo`) and `rest.repos` (repo discovery, `Repositories.ListByOrg`). `Runner` already knows which collection failed, so it passes the value; `SelfMetricsRecorder.RecordAPIError` gains an `endpoint string` parameter. Low cardinality, no URL paths.

### Resource attributes: attribute group + live-check matcher

Live-check matchers doc: "Do not compare a resource with an entity… An attribute group is the only thing it can be compared with." So the resource is declared as public attribute group `renovate_github_exporter.resource`, refs into the pinned OTel semconv dependency, and `.weaver.toml` holds a `sample_type = "resource"` matcher (`service.name == "renovate-github-exporter"`) with `strict_attribute_groups`. `service.name` required, `container.id` conditionally required (container detector), the rest recommended. User-supplied `OTEL_RESOURCE_ATTRIBUTES` are not declared; live-check runs without them.

Entity imports (`service`, `host`, …) with `entity_associations` were tried: they only add `improvement` findings about `service.version`/`service.criticality` and declare nothing the group doesn't. Rejected.

The Go attributes template would otherwise emit constants for the referenced OTel attributes (e.g. `AttrProcessCommandargs`); `templates/go/weaver.yaml` filters the resource group out. `provider.go` keeps using `go.opentelemetry.io/otel/semconv`.

### Live-check strictness: `--v2`

Measured on 0.28.0 with the current probe (pr.count carrying `github.issue.state` and `probe.undeclared`, plus metric `probe.undeclared.metric`):

- v1 mode (today): undeclared metric and undeclared attribute key are violations; `github.issue.state` on `github.pr.count` passes because v1 searches the whole registry.
- v2 mode (`--v2`): `sample_attribute.rs` only looks up the matched signal's attributes unless `--search-all-attributes`, so `github.issue.state` on `github.pr.count` becomes `[violation] Attribute 'github.issue.state' does not exist in the registry`. Missing required attributes are violations. Without a matcher the resource fails the same way, hence the matcher above.

`--include-unreferenced` is deprecated on 0.28 and dropped; v2 lookup goes through the signal and the matched group, which ref the dependency attributes directly.

### `weaver registry resolve` replacement

0.28 help: "resolve — DEPRECATED … Please use 'weaver registry generate' or 'weaver registry package' instead." `package` produces the resolved artifact (`manifest.yaml` + `resolved.yaml`); it requires `--v2` and `--resolved-registry-uri`. The workflow uploads the package directory as the artifact and passes the run URL as the URI.

## Risks / Trade-offs

- `definition/2` and v2 live-check are flagged "not yet stable" by weaver → pinned version; renovate bumps run the integration test.
- Resource detectors differ by environment (CI has no container) → `container.id` is conditionally required, the others recommended, so absence is not a violation.
- `RecordAPIError` signature change is internal (`internal/`), no external API.
