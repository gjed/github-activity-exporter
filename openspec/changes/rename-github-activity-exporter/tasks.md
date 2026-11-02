# Tasks

## 1. Go module

- [ ] 1.1 Change `go.mod` module path to `github.com/gjed/github-activity-exporter` and rewrite every import
- [ ] 1.2 Update meter scope names, `.golangci.yml` local prefix and `internal/semconv/doc.go`
- [ ] 1.3 `go build ./... && go test ./...`

## 2. Binary

- [ ] 2.1 `git mv cmd/renovate-exporter cmd/github-activity-exporter`; update `--version` output and the startup log line
- [ ] 2.2 Update `Makefile` `BINARY`/`CMD`, `.gitignore`, `Dockerfile` (build output, entrypoint, `CONFIG_FILE` path)

## 3. Registry, resource and dashboard

- [ ] 3.1 Registry manifest `name`/`description`/`schema_url`; rename resource group to `github_activity_exporter.resource`; update attribute examples
- [ ] 3.2 `service.name` in `internal/exporter/provider.go`; `.weaver.toml` matcher; `templates/go/weaver.yaml` filter
- [ ] 3.3 Dashboard template title/uid and `templates/grafana/weaver.yaml` file name
- [ ] 3.4 `make check-schema && make generate`; confirm the generated diff holds only renamed strings

## 4. Release and image

- [ ] 4.1 `image.yml`: publish to `ghcr.io/gjed/github-activity-exporter`
- [ ] 4.2 `package.json` name and description

## 5. Docs

- [ ] 5.1 README: title, badges, image, install command, config paths, one-line description
- [ ] 5.2 `AGENTS.md` title
- [ ] 5.3 Markdown lint on changed `.md` files

## 6. Verification

- [ ] 6.1 `rg -n 'renovate-exporter|renovate-github-exporter|renovate_exporter'`: every hit justified
- [ ] 6.2 `docker build .` and `docker run <image> --version`
