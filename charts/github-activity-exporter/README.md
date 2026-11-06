# github-activity-exporter Helm chart

Deploys [github-activity-exporter](https://github.com/gjed/github-activity-exporter) as a single hardened pod.

```bash
helm install github-activity-exporter oci://ghcr.io/gjed/charts/github-activity-exporter \
  --version X.Y.Z -f values.yaml
```

The chart version and `appVersion` equal the exporter release, and `image.tag` defaults to `appVersion`. Installing from a clone of the repository needs `--set image.tag=X.Y.Z`: the chart in the repository carries `0.0.0` placeholders.

Examples:

- [`examples/renovate-values.yaml`](examples/renovate-values.yaml): Renovate PRs and the Dependency Dashboard of one org, with a PAT.
- [`examples/github-app-values.yaml`](examples/github-app-values.yaml): an org through a GitHub App, with the Prometheus bridge, a ServiceMonitor and the Grafana dashboard.

## Config

`config` is the exporter's [config file](https://github.com/gjed/github-activity-exporter#config-file), rendered verbatim into a ConfigMap mounted at `/etc/github-activity-exporter/config.yaml`. The chart does not model its keys, so any key the exporter understands, including keys added after this chart version, works without a chart change. A checksum of the config is a pod annotation, so changing it replaces the pod.

The chart reads two keys to check they match the ports it wires, and fails rendering otherwise:

| Config key                                                 | Chart value    | Default |
| ---------------------------------------------------------- | -------------- | ------- |
| `health.listen_address`                                    | `health.port`  | `10062` |
| `export.prometheus.listen_address` (when the bridge is on) | `metrics.port` | `9090`  |

The ConfigMap is not a Secret: keep tokens out of `config` and reference them by env var or file instead.

## Secrets

Secret material comes from one Secret: `secret.existingSecret`, or a Secret the chart creates from `secret.data` when `secret.create` is true. A chart-managed Secret stores the values in the Helm release; prefer an existing Secret in production.

- `secret.env` maps env var names to Secret keys.
- `secret.files` lists Secret keys mounted read-only at `/etc/github-activity-exporter/secrets/<key>`, readable by the container user only.

| Credential                 | Config                                                                   | Secret wiring                              |
| -------------------------- | ------------------------------------------------------------------------ | ------------------------------------------ |
| PAT, shared                | no `auth` block                                                          | `env: {GITHUB_TOKEN: <key>}`               |
| PAT, per target            | `auth.token_env: ACME_TOKEN`                                             | `env: {ACME_TOKEN: <key>}`                 |
| GitHub App key, file       | `auth.app.private_key_path: /etc/github-activity-exporter/secrets/<key>` | `files: [<key>]`                           |
| GitHub App key, base64 PEM | `auth.app.private_key_env: GITHUB_APP_KEY`                               | `env: {GITHUB_APP_KEY: <key>}`             |
| OTLP headers               | no `export.otlp.headers`                                                 | `env: {OTEL_EXPORTER_OTLP_HEADERS: <key>}` |

`OTEL_EXPORTER_OTLP_HEADERS` holds `name=value` pairs separated by commas, for example `authorization=Bearer <token>,x-scope-orgid=my-tenant`. Headers set in `export.otlp.headers` replace it entirely, so set headers in one place only.

The checksum annotation covers a chart-managed Secret only. After rotating an existing Secret, run `kubectl rollout restart deployment/<release>-github-activity-exporter`.

For anything else, `env`, `envFrom`, `extraVolumes` and `extraVolumeMounts` are passed to the pod as is, for example the standard `OTEL_RESOURCE_ATTRIBUTES`.

## Probes

The liveness probe calls `/healthz` and the readiness probe `/readyz` on the health port. No Service exposes that port.

| Probe     | Path       | `periodSeconds` | `timeoutSeconds` | `failureThreshold` |
| --------- | ---------- | --------------- | ---------------- | ------------------ |
| liveness  | `/healthz` | 30              | 5                | 3                  |
| readiness | `/readyz`  | 10              | 5                | 3                  |

The exporter does the stall detection itself: `/healthz` fails only when a collection loop has not completed a cycle within `health.stall_timeout` (default `2 × collection_interval + 1h`, 1h10m at the default 5m), or after `health.unauthorized_cycles` cycles answered only with 401. The probe thresholds only absorb transient errors, so a stalled pod restarts after `stall_timeout` plus at most 90 seconds. To restart sooner, lower `health.stall_timeout` in `config` (it must exceed `collection_interval`) rather than the probe thresholds. Raising `collection_interval` raises the default `stall_timeout` with it.

`/healthz` answers 200 during startup, so there is no startup probe. `/readyz` answers 503 until every target has completed its first collection cycle, which can last as long as a GitHub rate-limit pause on large orgs. Readiness only gates the bridge Service endpoints and never restarts the pod, but `helm install --wait` waits for it: give `--timeout` enough room.

## Prometheus bridge

With `config.export.prometheus.enabled: true`, set `metrics.service.enabled: true` for a Service on `metrics.port`, and `metrics.serviceMonitor.enabled: true` for a Prometheus Operator ServiceMonitor scraping `/metrics` through it.

## Replicas

The Deployment runs exactly one replica and the count is not configurable. Two exporters for the same targets push and expose the same counters twice, doubling every count. The `Recreate` strategy keeps the old and the new pod from overlapping during upgrades, so a rollout has a short gap in collection instead.

## Security

The pod runs as UID/GID 65532 with a read-only root filesystem, no privilege escalation, all capabilities dropped, the `RuntimeDefault` seccomp profile and no service account token. It is admitted by the `restricted` Pod Security Standard.

## Grafana dashboard

`dashboard.enabled: true` creates a ConfigMap labelled `grafana_dashboard: "1"` for the Grafana dashboard sidecar, carrying the generated [`dashboards/github-activity.json`](https://github.com/gjed/github-activity-exporter/blob/main/dashboards/github-activity.json) of the chart's version. Change `dashboard.labels` and `dashboard.annotations` to match the sidecar's label selector and folder annotation.
