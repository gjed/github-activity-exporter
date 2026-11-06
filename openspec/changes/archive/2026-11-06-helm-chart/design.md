# Design

## Context

The image is `gcr.io/distroless/static:nonroot` (UID/GID 65532) with `CONFIG_FILE=/etc/github-activity-exporter/config.yaml`. Config is one YAML file; credentials are referenced from it by env var name (`token_env`, `private_key_env`) or file path (`private_key_path`), or default to `GITHUB_TOKEN`. The health server always listens on `health.listen_address` (default `:10062`); the Prometheus bridge, when enabled, on `export.prometheus.listen_address` (default `:9090`). OTLP exporters are built with the OTel Go SDK, which reads the standard `OTEL_EXPORTER_OTLP_*` env vars before applying the options from config. Helm in use is v4.

## Goals / Non-Goals

**Goals:**

- One chart that never needs a release to follow new config keys.
- Secrets never in the ConfigMap.
- Defaults that pass the `restricted` Pod Security Standard.

**Non-Goals:**

- HPA, PodDisruptionBudget, NetworkPolicy, Ingress: one replica, no inbound traffic except scrapes.
- A `values.schema.json` for `config`: it would model the config keys.
- Exposing the health port outside the pod ([#77](https://github.com/gjed/github-activity-exporter/issues/77) may add debug endpoints there).

## Decisions

### Config is a free-form map

`config` is rendered with `toYaml` into `config.yaml`. The chart reads two keys only to validate, never to write: `health.listen_address` and `export.prometheus.{enabled,listen_address}`, so that probes and Services cannot silently point at the wrong port. The port values (`health.port`, `metrics.port`) stay chart values because Kubernetes needs them as numbers. Alternative rejected: generating `listen_address` from `health.port` would write into the config and fight a user-set value.

The ConfigMap is mounted with `subPath` at the file path, so the `secrets/` directory can be mounted next to it. `subPath` mounts never update in place; the checksum annotation replaces the pod instead.

### Secret wiring through one Secret with explicit maps

```yaml
secret:
  existingSecret: ""   # or
  create: false
  data: {}             # stringData of the chart-managed Secret
  env: {}              # ENV_VAR: secret key
  files: []            # secret keys mounted under /etc/github-activity-exporter/secrets/
```

This covers every credential form the exporter reads: `GITHUB_TOKEN` and `token_env` vars, `private_key_env` (base64 PEM), `private_key_path` (file), and OTLP headers through `OTEL_EXPORTER_OTLP_HEADERS`. The env-var route for headers needs no exporter change: when `export.otlp.headers` is empty the exporter passes no header option and the SDK's env headers apply. When it is set it replaces the env headers entirely, so the two must not be mixed; the chart README says so. Alternatives rejected: fixed fields such as `githubToken` / `appPrivateKey` (model the auth forms and miss multi-target setups with several `token_env` vars), and mounting the whole Secret as files (exposes keys meant for env).

Secret files are mounted with mode `0440` and `fsGroup: 65532`, so only the container user can read them. A chart-managed Secret's checksum is added to the pod template; an existing Secret's changes need a manual restart (documented).

### Probes leave stall detection to the exporter

`/healthz` already waits `health.stall_timeout` (default `2 × collection_interval + 1h`) before failing, and `health.unauthorized_cycles` cycles before failing on 401s. The probes therefore only debounce transient errors:

| Probe     | Path       | period | timeout | failureThreshold |
| --------- | ---------- | ------ | ------- | ---------------- |
| liveness  | `/healthz` | 30s    | 5s      | 3                |
| readiness | `/readyz`  | 10s    | 5s      | 3                |

A stalled loop is restarted after `stall_timeout + up to 90s`. To restart sooner, lower `health.stall_timeout` (it must exceed `collection_interval`), not the probe thresholds. No startup probe: `/healthz` is 200 during startup, and `/readyz` stays 503 until the first cycle, which can take as long as a rate-limit pause; readiness only gates the bridge Service endpoints, so a long first cycle never restarts the pod. `helm install --wait` waits for that first cycle; the README says so.

### Single replica, Recreate

`replicas: 1` is hardcoded and the strategy is `Recreate`: two pods would each push and expose the same counters, doubling them, and `RollingUpdate` would overlap the old and new pod during upgrades.

### Dashboard through a symlink

`charts/github-activity-exporter/dashboards/github-activity.json` is a relative symlink to `../../../dashboards/github-activity.json`. Helm follows symlinks when linting, templating and packaging (verified with Helm v4.3.1: the packaged `.tgz` contains the file contents), so the chart reads the generated file with `.Files.Get` and the packaged chart carries the dashboard of its version. Alternative rejected: copying the file in `make generate`, a second generated artifact for the same content.

### Chart version set at release time

`Chart.yaml` holds `version: 0.0.0` and `appVersion: "0.0.0"`. The publish job runs `helm package --version X.Y.Z --app-version X.Y.Z` from the release tag, so nothing is committed to `main` at release (the `ci-release` rule). A local install from the repo must set `image.tag`.

### CI in one chart workflow

`.github/workflows/chart.yml`:

- `check`, on `pull_request` with `paths` (`charts/**`, `dashboards/github-activity.json`, the workflow): `helm lint --strict`, then `helm template | kubeconform -strict -summary` with default values and each example file. The ServiceMonitor schema comes from the datreeio CRDs-catalog. Path filters live in a separate workflow because `ci.yml` runs on every PR.
- `publish`, on `release: published` and `workflow_dispatch` from a `vX.Y.Z` tag ref, mirroring `image.yml`: derive the version from the tag, package, `helm registry login ghcr.io` with `GITHUB_TOKEN`, `helm push oci://ghcr.io/gjed/charts`. It runs independently of the image job, so a failure in either is re-run alone.

Actions are pinned by SHA with the version comment, and the Helm and kubeconform versions carry `# renovate:` comments.

## Risks / Trade-offs

- [GHCR creates the chart package private on the first push] → make it public once in the package settings after the first release.
- [Values reference secret keys missing from an existing Secret] → the pod fails with `CreateContainerConfigError` naming the key; nothing the chart can check at render time.
- [Config checksum does not cover an external Secret] → documented; `kubectl rollout restart` after rotating credentials.
- \[`--set` of a port in the config without the matching chart value\] → render fails with the mismatch, the intended trade-off for not writing config keys.
