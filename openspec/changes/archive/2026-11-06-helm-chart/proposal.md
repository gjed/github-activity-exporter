# Proposal

## Why

The only deployment artifact is the container image, so every Kubernetes user hand-writes the manifests, the secret wiring and the probes. Kubernetes is the main target for a long-running exporter, and the first real deployment is an org cluster, so the chart has to be production-usable from its first release ([#20](https://github.com/gjed/github-activity-exporter/issues/20)).

## What Changes

- New Helm chart at `charts/github-activity-exporter`:
  - Deployment of `ghcr.io/gjed/github-activity-exporter`, image tag defaulting to the chart `appVersion` and overridable.
  - `values.config` is a free-form map rendered verbatim into a ConfigMap and mounted at `/etc/github-activity-exporter/config.yaml`. The chart does not model config keys, so new keys (for example the `telemetry` section of [#73](https://github.com/gjed/github-activity-exporter/issues/73)) need no chart change. A config checksum annotation restarts the pod on change.
  - Credentials and OTLP headers from one Secret, either existing or chart-managed, exposed as env vars (`GITHUB_TOKEN`, a `token_env` var, `private_key_env`, `OTEL_EXPORTER_OTLP_HEADERS`) and/or files (`private_key_path`). Generic `env`, `envFrom`, `extraVolumes` and `extraVolumeMounts` for anything else.
  - Liveness on `/healthz` and readiness on `/readyz` on the health port (default `10062`), with thresholds documented against `health.stall_timeout` and `collection_interval`. The health port has no Service.
  - Optional Service for the Prometheus bridge and optional ServiceMonitor for `/metrics`.
  - Hardened pod: non-root, read-only root filesystem, all capabilities dropped, `RuntimeDefault` seccomp, no service account token, resource requests and limits, one replica with the `Recreate` strategy.
  - Optional Grafana dashboard ConfigMap (`grafana_dashboard` label) embedding the generated `dashboards/github-activity.json` through `.Files.Get` on a symlink, not a copy.
  - Chart README and two example values files: a Renovate preset with a PAT, and a GitHub App for an org.
- CI: `helm lint`, `helm template` and `kubeconform -strict` on PRs touching the chart.
- Release: when a GitHub Release is published, the chart is packaged with version and `appVersion` equal to the release version and pushed to `oci://ghcr.io/gjed/charts/github-activity-exporter`.
- README: a "Kubernetes" section with `helm install` from the OCI registry.

## Capabilities

### New Capabilities

- `helm-chart`: Kubernetes deployment of the exporter through the Helm chart: config passthrough, secret wiring, probes, services, hardening and the dashboard ConfigMap.

### Modified Capabilities

- `ci-checks`: adds the chart lint/template/schema check on PRs that touch the chart.
- `ci-release`: adds publishing the chart as an OCI artifact on GHCR for each release.

## Impact

- New: `charts/github-activity-exporter/`, `.github/workflows/chart.yml`.
- Changed: `README.md` (Kubernetes section, CI table).
- No Go code change. No change to the image or to semantic-release.
- New GHCR package `ghcr.io/gjed/charts/github-activity-exporter`. GHCR creates it private on the first push; it has to be made public once by hand.
