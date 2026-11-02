## 1. Registry

- [x] 1.1 Add `file_format: definition_manifest/2.0` to `registry/manifest.yaml`
- [x] 1.2 Add `github.api.endpoint` enum and the `renovate_github_exporter.resource` group to `registry/attributes.yaml`
- [x] 1.3 Add `requirement_level: recommended` and the `attributes:` block from design.md to every metric in `registry/signals.yaml`
- [x] 1.4 Filter the resource group out of `templates/go/weaver.yaml`; add casing replaces for the endpoint enum in `templates/go/attributes.go.j2`
- [x] 1.5 `make generate`; `make check-schema` shows no `requirement_level` warnings

## 2. Endpoint attribute in code

- [x] 2.1 `SelfMetrics.RecordAPIError(ctx, target, endpoint)` records `github.api.endpoint`
- [x] 2.2 `Runner` passes `graphql` for PR failures, `rest.issues` for issue/dashboard failures and `rest.repos` for repo discovery failures; update `SelfMetricsRecorder` and test fakes
- [x] 2.3 Unit test asserts the endpoint attribute on `github_exporter.api.errors`

## 3. Live-check strictness

- [x] 3.1 Add `.weaver.toml` with the resource matcher
- [x] 3.2 `tests/integration/live_check_test.go`: weaver 0.28.0, `--v2`, `--config .weaver.toml`, drop `--include-unreferenced`; conforming emitter sends all required attributes
- [x] 3.3 Negative tests: undeclared metric, attribute declared elsewhere but not on the metric

## 4. CI

- [x] 4.1 `registry-validate.yml`: replace `weaver registry resolve` with `weaver registry package --v2`, upload the package directory

## 5. Spec

- [x] 5.1 Validate: `go build ./... && go test ./... && make check-schema && make generate`, integration tests, `git diff` shows only expected generated changes
