# Tasks

## 1. Config schema

- [x] 1.1 Add `Export`, `OTLP`, `TLS`, `Prometheus` structs and `CollectionInterval` to `config.Config` in `internal/config/config.go`; verify `go build ./...` passes
- [x] 1.2 Set viper defaults for `collection_interval` (`5m`), `export.otlp.protocol` (`http`) and `export.prometheus.listen_address` (`:9090`); verify a test loading a minimal file asserts those values
- [x] 1.3 Validate in `validate`: required `export.otlp.endpoint`, protocol in {`http`, `grpc`}, `collection_interval > 0`; verify table tests in `config_test.go` cover each error
- [x] 1.4 Add `export.otlp.endpoint` to every existing `internal/config/testdata/*.yaml` and add testdata for a full export section (multiple headers, grpc, tls insecure, prometheus enabled with custom address); verify `go test ./internal/config/...` passes
- [x] 1.5 Validate the endpoint shape per protocol (http: http(s) URL with host; grpc: no scheme); verify table tests cover both error cases

## 2. Wiring

- [x] 2.1 Build `exporter.Config` from `cfg.Export` and `cfg.CollectionInterval` in `cmd/renovate-exporter/main.go`; remove reads of `OTLP_ENDPOINT`, `OTLP_PROTOCOL`, `OTLP_AUTH_HEADER`, `OTLP_TLS_INSECURE`, `COLLECTION_INTERVAL`, `PROMETHEUS_ENABLED`, `PROMETHEUS_ADDR` and the now-unused `mustDuration`; verify `go build ./...` and a grep for the removed names in `cmd/` returns nothing
- [x] 2.2 Verify a config without `export.otlp.endpoint` makes the binary exit non-zero with an error naming the key (run the built binary against a temp config)

## 3. Documentation

- [x] 3.1 Update `README.md`: quick start config and `docker run` (drop `-e OTLP_ENDPOINT`/`PROMETHEUS_ENABLED`), the config file example with the `export` section and `collection_interval`, and drop the removed rows from the env var table; verify markdown lint passes on `README.md`

## 4. Integration

- [x] 4.1 Run `go build ./... && go test ./... && make check-schema`; verify all pass
