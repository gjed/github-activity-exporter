# helm-chart Specification

## Purpose

Deploy the exporter on Kubernetes through a Helm chart that passes the config file through unchanged, wires credentials from Secrets, probes the health endpoints and runs a single hardened replica.

## Requirements

### Requirement: Config passthrough

The chart SHALL render `values.config` verbatim as YAML into a ConfigMap mounted read-only at `/etc/github-activity-exporter/config.yaml`. It SHALL NOT model, rename, default or drop config keys. The pod template SHALL carry a checksum of the rendered config.

#### Scenario: Unknown key passes through

- **WHEN** `values.config` contains a key the chart has never seen, for example `telemetry: {enabled: true}`
- **THEN** the mounted config file contains that key with the same value

#### Scenario: Config change restarts the pod

- **WHEN** any value under `values.config` changes and the release is upgraded
- **THEN** the pod template checksum annotation changes and the pod is replaced

### Requirement: Image

The chart SHALL run `ghcr.io/gjed/github-activity-exporter` with the tag `image.tag`, defaulting to the chart `appVersion` when `image.tag` is empty.

#### Scenario: Default tag

- **WHEN** the chart with `appVersion` `X.Y.Z` is installed with no `image.tag`
- **THEN** the container image is `ghcr.io/gjed/github-activity-exporter:X.Y.Z`

#### Scenario: Overridden tag

- **WHEN** `image.tag` is `latest`
- **THEN** the container image is `ghcr.io/gjed/github-activity-exporter:latest`

### Requirement: Secret source

Secret material SHALL come from one Secret: `secret.existingSecret`, or a chart-managed Secret built from `secret.data` when `secret.create` is true. Setting both, or referencing secret keys with neither, SHALL fail rendering with an error naming the values.

#### Scenario: Existing Secret

- **WHEN** `secret.existingSecret` is `exporter-creds` and `secret.env` maps `GITHUB_TOKEN` to key `github-token`
- **THEN** the container env `GITHUB_TOKEN` reads key `github-token` of Secret `exporter-creds` and the chart creates no Secret

#### Scenario: Chart-managed Secret

- **WHEN** `secret.create` is true and `secret.data` holds `github-token`
- **THEN** the chart creates a Secret with that key and the pod reads it

#### Scenario: Conflicting sources

- **WHEN** `secret.create` is true and `secret.existingSecret` is set
- **THEN** rendering fails naming both values

### Requirement: Secret exposure as env and files

Each entry of `secret.env` SHALL become a container env var read from the given Secret key. Each key listed in `secret.files` SHALL be mounted read-only at `/etc/github-activity-exporter/secrets/<key>`, readable by the container user and no other user.

#### Scenario: OTLP headers from the Secret

- **WHEN** `secret.env` maps `OTEL_EXPORTER_OTLP_HEADERS` to a key holding `authorization=Bearer x`
- **THEN** the exporter sends that header on OTLP requests, given that `export.otlp.headers` is not set in the config

#### Scenario: GitHub App key as a file

- **WHEN** `secret.files` lists `app-key.pem` and the config sets `auth.app.private_key_path: /etc/github-activity-exporter/secrets/app-key.pem`
- **THEN** the exporter reads the private key from that file

### Requirement: Generic pod extensions

The chart SHALL pass `env`, `envFrom`, `extraVolumes` and `extraVolumeMounts` through to the pod unchanged.

#### Scenario: Standard OTel env var

- **WHEN** `env` contains `OTEL_RESOURCE_ATTRIBUTES=deployment.environment=prod`
- **THEN** the container has that env var

### Requirement: Health probes

The container SHALL expose the health port (`health.port`, default `10062`), with a liveness probe on `/healthz` and a readiness probe on `/readyz`. Probe timings SHALL be overridable. No Service SHALL expose the health port.

#### Scenario: Default probes

- **WHEN** the chart is installed with default values
- **THEN** liveness requests `GET /healthz` and readiness requests `GET /readyz` on port `10062`, and no Service targets port `10062`

#### Scenario: Health port mismatch

- **WHEN** the port of `config.health.listen_address`, or of the exporter default `:10062` when it is unset, differs from `health.port`
- **THEN** rendering fails naming both values

### Requirement: Prometheus bridge Service and ServiceMonitor

When `metrics.service.enabled` is true the chart SHALL create a Service for port `metrics.port` (default `9090`); when `metrics.serviceMonitor.enabled` is true it SHALL create a ServiceMonitor scraping `/metrics` through that Service. Both SHALL be off by default.

#### Scenario: Bridge Service

- **WHEN** `config.export.prometheus.enabled` and `metrics.service.enabled` are true
- **THEN** a Service exposes port `9090` and its endpoint serves `/metrics`

#### Scenario: Service without bridge

- **WHEN** `metrics.service.enabled` is true and `config.export.prometheus.enabled` is not true
- **THEN** rendering fails naming both values

#### Scenario: ServiceMonitor without Service

- **WHEN** `metrics.serviceMonitor.enabled` is true and `metrics.service.enabled` is false
- **THEN** rendering fails naming both values

### Requirement: Single replica

The Deployment SHALL run exactly one replica with the `Recreate` strategy, and the replica count SHALL NOT be configurable, because two exporters for the same targets double every counter.

#### Scenario: Upgrade never overlaps pods

- **WHEN** the release is upgraded
- **THEN** the old pod terminates before the new one starts

### Requirement: Hardened pod

The pod SHALL run as a non-root user with a read-only root filesystem, no privilege escalation, all capabilities dropped, the `RuntimeDefault` seccomp profile, no mounted service account token, and default resource requests and limits.

#### Scenario: Restricted Pod Security Standard

- **WHEN** the chart is installed with default values into a namespace enforcing the `restricted` Pod Security Standard
- **THEN** the pod is admitted

### Requirement: Grafana dashboard ConfigMap

When `dashboard.enabled` is true the chart SHALL create a ConfigMap labelled `grafana_dashboard: "1"` (overridable) whose content is the repository's generated `dashboards/github-activity.json`, byte for byte.

#### Scenario: Dashboard matches the generated file

- **WHEN** `dashboard.enabled` is true
- **THEN** the ConfigMap data `github-activity.json` equals `dashboards/github-activity.json` at the chart's version
