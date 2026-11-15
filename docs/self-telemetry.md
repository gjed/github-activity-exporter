# Self-telemetry

Self-telemetry is telemetry about the exporter itself: metrics, traces, logs and profiles. It is separate from the GitHub metrics the exporter exports, and it is off by default.

```yaml
telemetry:
  enabled: false        # global toggle; false turns every signal below off
  metrics:
    enabled: true
  traces:
    enabled: true
  logs:
    enabled: true
  profiles:
    enabled: false      # profiles are opt-in, see "Profiles"
```

A signal is active only when `telemetry.enabled` and its own `enabled` are both true, `OTEL_SDK_DISABLED` is not `true`, and, for traces, metrics and logs, `OTEL_<SIGNAL>_EXPORTER` is not `none`. Environment variables can turn a signal off, never on. Every key can be overridden with `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_ENABLED`, `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_<SIGNAL>_ENABLED` and `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_PROFILES_{ENDPOINT,INTERVAL,MUTEX_FRACTION,BLOCK_RATE}`; a value of the wrong type is a startup error. The startup log lists the active signals under `self_telemetry`.

Metrics are described in [Self-telemetry metrics](#self-telemetry-metrics), traces in [Traces](#traces), logs in [Logs](#logs) and profiles in [Profiles](#profiles).

Self-telemetry runs on its own OTel SDK providers. No `telemetry` key and no `OTEL_*` variable other than `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` changes what the GitHub metrics export, where, how or when: their OTLP push is configured by `export.otlp` only. The `github_exporter.*` metrics (`scrape.duration`, `scrape.overruns`, `api.errors`, `api.secondary_rate_limits`, `otlp.errors`, `api.rate_limit.*`) belong to the GitHub metrics and are always on; the exporter dashboard uses all of them. When the Prometheus bridge is enabled, self-telemetry metrics are also served on `/metrics`.

Traces, metrics and logs go over OTLP. With no OTLP endpoint in the environment, each signal uses `export.otlp`: same endpoint, protocol, headers (`export.otlp.headers_env` included) and TLS. For `protocol: http` the endpoint is a base URL, and a trailing `/v1/metrics` is dropped, so `https://gw.example.com/otlp` and `https://gw.example.com/otlp/v1/metrics` both send traces to `https://gw.example.com/otlp/v1/traces`. Once `OTEL_EXPORTER_OTLP_ENDPOINT` or `OTEL_EXPORTER_OTLP_<SIGNAL>_ENDPOINT` is set, that signal is configured from the `OTEL_EXPORTER_OTLP_*` variables only, and `export.otlp` headers are never sent to it.

The standard [OTel SDK variables](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/) configure self-telemetry, signal-specific before generic. Only `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` also apply to the GitHub metrics:

| Env                                                                                | Effect                                                                                                           |
| ---------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `OTEL_SDK_DISABLED`                                                                | `true` turns all self-telemetry off. The GitHub metrics keep exporting                                           |
| `OTEL_TRACES_EXPORTER`, `OTEL_METRICS_EXPORTER`, `OTEL_LOGS_EXPORTER`              | `none` turns the signal off; `otlp` is the default. Other values log a warning and mean `otlp`                   |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_ENDPOINT` | Destination; replaces `export.otlp` for the signal                                                               |
| `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_PROTOCOL` | `grpc` or `http/protobuf` (default) when an endpoint variable is set. Ignored when the signal uses `export.otlp` |
| `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_HEADERS`   | Request headers; replace the inherited `export.otlp` headers                                                     |
| `OTEL_EXPORTER_OTLP_TIMEOUT`, `OTEL_EXPORTER_OTLP_{TRACES,METRICS,LOGS}_TIMEOUT`   | Export timeout in milliseconds                                                                                   |
| `OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG`                                   | Trace sampler; default `parentbased_always_on`                                                                   |
| `OTEL_METRIC_EXPORT_INTERVAL`                                                      | Self-telemetry metric push interval in milliseconds; default 60000. Only with an OTLP metrics endpoint variable  |
| `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES`                                    | Resource attributes, for the GitHub metrics too                                                                  |

Every provider, the GitHub metrics included, shares one resource: `service.name` (default `github-activity-exporter`), `service.version` (the build version), `service.instance.id` (a random UUID per process) and the host, OS, process and container attributes. `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` override them. The instance ID changes on every restart; set a stable one, such as the pod name, with `OTEL_RESOURCE_ATTRIBUTES=service.instance.id=<id>` if your backend derives `instance` from it. Every meter, tracer and logger uses the instrumentation scope `github.com/gjed/github-activity-exporter` with the build version.

On SIGTERM or SIGINT the exporter flushes and shuts down every self-telemetry provider after the GitHub metrics, within the same 30 s deadline.

## Self-telemetry metrics

With self-telemetry metrics active ([#74](https://github.com/gjed/github-activity-exporter/issues/74)), the exporter records, on OTLP and on `/metrics` when the bridge is enabled:

- Go runtime metrics, per the [Go runtime semantic conventions](https://opentelemetry.io/docs/specs/semconv/runtime/go-metrics/): `go.memory.used` (by `go.memory.type`), `go.memory.limit` (only when a memory limit such as `GOMEMLIMIT` is set), `go.memory.allocated`, `go.memory.allocations`, `go.memory.gc.goal`, `go.goroutine.count`, `go.processor.limit`, `go.config.gogc` and `go.schedule.duration`.
- `http.client.request.duration` and `http.client.request.body.size` for every GitHub request (REST, GraphQL, the startup credential check and the GitHub App token exchange), per the [HTTP client semantic conventions](https://opentelemetry.io/docs/specs/semconv/http/http-metrics/#http-client). Attributes: `http.request.method`, `http.response.status_code`, `server.address`, `server.port`, `url.scheme`, `network.protocol.name`, `network.protocol.version`, `error.type` (the status code for 4xx and 5xx, the error type when no response arrives) and `url.template`.

`url.template` is set only for the routes the exporter calls, under the target's API root (`/api/v3` on GitHub Enterprise Server): `/graphql` (`/api/graphql` on GHES), `/orgs/{org}/repos`, `/user`, `/app` and `/app/installations/{installation_id}/access_tokens`. Other paths get no `url.template`. No attribute carries a full URL, an org, repo or installation, or a token. A request's duration is one round trip to GitHub: it does not include the wait on a rate limit pool paused below its threshold, or the wait for a token.

With self-telemetry metrics inactive, no `go.*` or `http.*` metric is exported and the GitHub client is not instrumented. The registry imports these metrics from the OTel semantic conventions in [`registry/telemetry.yaml`](../registry/telemetry.yaml).

When the self-telemetry metrics inherit `export.otlp`, they travel in the GitHub metrics push, every `export.interval` and after each cycle, and `OTEL_METRIC_EXPORT_INTERVAL` does not apply. One push carries the shared resource, so its `target_info` series has one writer and increasing timestamps: the backend needs no out-of-order ingestion. The e2e runs a stock Prometheus 3.x, without `out_of_order_time_window`, and checks that it rejects no sample ([#99](https://github.com/gjed/github-activity-exporter/issues/99)).

With `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` or `OTEL_EXPORTER_OTLP_ENDPOINT` set, the self-telemetry metrics are pushed on their own, every `OTEL_METRIC_EXPORT_INTERVAL`. If that endpoint is the backend of `export.otlp`, both pushes write the same `target_info` series on separate schedules, and the backend needs out-of-order ingestion (Prometheus `storage.tsdb.out_of_order_time_window`, Mimir and Grafana Cloud Metrics the per-tenant `out_of_order_time_window`) or it rejects pushes, GitHub metric pushes included, with `out of order sample`.

## Traces

With self-telemetry traces active ([#75](https://github.com/gjed/github-activity-exporter/issues/75)), each collection cycle of a target is one trace, exported over OTLP:

```text
github_exporter.cycle                      exporter.target, github_exporter.cycle.repos, github_exporter.cycle.outcome
├── GET /orgs/{org}/repos                  discovery, first cycle only
├── github_exporter.collect                github_exporter.collector (pr, issue, dashboard), github_exporter.failed_repos
│   ├── github_exporter.repo               github.org, github.repo: one per repo (PR scan, issue title-filter listing)
│   │   ├── github_exporter.rate_limit.wait  github.api.rate_limit.resource, github_exporter.rate_limit.reason (primary, secondary)
│   │   └── POST /graphql                  one per request and per attempt
│   └── github_exporter.batch              github_exporter.batch.size, github_exporter.failed_repos: one per issue or dashboard batch
└── github_exporter.flush                  the push of the cycle's metrics
```

GitHub requests are HTTP client spans per the [HTTP client span conventions](https://opentelemetry.io/docs/specs/semconv/http/http-spans/#http-client), named `{method} {url.template}` (`{method}` for a path with no `url.template`, see [Self-telemetry metrics](#self-telemetry-metrics)), so the name carries the API root on GitHub Enterprise Server (`POST /api/graphql`). GraphQL requests carry `graphql.operation.type` and, for the batched queries, `graphql.operation.name` (`IssueCountBatch`, `DashboardBatch`); a retried request carries `http.request.resend_count`. A request that waits for a paused pool or a secondary rate limit pause records a `github_exporter.rate_limit.wait` span lasting as long as the wait. No trace context is sent to GitHub.

A failed span has status `Error` and `error.type`: the GraphQL error type of a batched query (`NOT_FOUND`), the HTTP status code, or `_OTHER`. Exceptions are not recorded as span events, which the semantic conventions deprecate. The exporter never traces its own OTLP exports. The startup credential and permission checks, the background repo discovery refresh and the GitHub App token exchange send requests outside a cycle: each request is a one-span trace. Span names and attributes are declared in [`registry/traces.yaml`](../registry/traces.yaml).

With traces inactive, the exporter uses the no-op tracer, creates no trace exporter and opens no connection for traces; the GitHub metrics are the same either way.

Traces are sampled once per cycle by `OTEL_TRACES_SAMPLER` and `OTEL_TRACES_SAMPLER_ARG` (default `parentbased_always_on`), so a cycle is kept or dropped whole. A cycle has about 3 spans per repo (the repo span and its two PR scan requests) plus 2 per batch, plus one per extra page, retry and rate limit wait. Measured on a 392-repo org with the Dependency Dashboard enabled, at the default `collection.concurrency` and `graphql_batch_size`: 877 spans for the first cycle, which has no event scan, and about 1,270 per cycle after it, about 366,000 spans a day per target at the default `collection_interval` of 5m. For a large org, sample a fraction of the cycles:

```yaml
env:
  - name: OTEL_TRACES_SAMPLER
    value: parentbased_traceidratio
  - name: OTEL_TRACES_SAMPLER_ARG
    value: "0.1"   # one cycle in ten
```

## Logs

The exporter logs JSON lines to stdout in every mode. With self-telemetry logs active ([#76](https://github.com/gjed/github-activity-exporter/issues/76)), each line is also an OTLP log record with the message as body, the same attributes, the shared resource and the instrumentation scope `github.com/gjed/github-activity-exporter`. The level is `info` for both outputs. The severity follows the [OTel log data model](https://opentelemetry.io/docs/specs/otel/logs/data-model/#field-severitynumber): `DEBUG`, `INFO`, `WARN` and `ERROR` are SeverityNumber 5, 9, 13 and 17, with the level as SeverityText.

With traces active too, a line logged inside a span carries the span's trace and span IDs: as the trace context of the OTLP record, and as `trace_id` and `span_id` on the stdout line. The cycle lines carry the cycle span, a failed metrics flush its `github_exporter.flush` span, a failed collector its `github_exporter.collect` span, a failed repo its `github_exporter.repo` or `github_exporter.batch` span, and the rate limit lines the span of the repo or batch whose request hit the limit. Lines outside a cycle, such as startup lines, carry neither.

Attribute keys are the registry attribute names, on stdout too:

| Key before [#76](https://github.com/gjed/github-activity-exporter/issues/76) | Key now                                 |
| ---------------------------------------------------------------------------- | --------------------------------------- |
| `target`                                                                     | `exporter.target`                       |
| `repo` (`owner/name`)                                                        | `github.org` and `github.repo` (`name`) |
| `owner`                                                                      | `github.org`                            |
| `resource` (rate limit pool)                                                 | `github.api.rate_limit.resource`        |

Some lines stay on stdout only: lines logged before self-telemetry starts (config, health server), the Prometheus bridge and profiles lines, and the errors of the OTLP exporters themselves (`OTLP export failed`, the unauthorized push hint, `opentelemetry error`, `shutdown error`, `self-telemetry shutdown error`), so an OTLP backend that is down never gets its own export errors queued for export. A cycle's `metrics flush failed` is exported with its flush span. While the backend is down, the batch processor drops records once its queue is full; stdout keeps them all. No line, on either output, carries a GitHub token, a GitHub App key or an OTLP header value.

With logs inactive, the exporter creates no log exporter and opens no connection for logs, and the stdout lines have no `trace_id` or `span_id`.

## Profiles

The OTel profiles signal is alpha and the OTel Go SDK has no profiles API, so the exporter does not produce OTLP profiles itself. It pushes Go pprof profiles of its own process to the [`pprof` receiver](https://github.com/open-telemetry/opentelemetry-collector-contrib/tree/main/receiver/pprofreceiver) of an OpenTelemetry Collector, which converts them to OTLP profiles. Profiles are off by default:

```yaml
telemetry:
  enabled: true
  profiles:
    enabled: true
    endpoint: http://otel-collector.monitoring.svc:4040  # pprof receiver, server mode
    interval: 60s        # CPU profile window and push period
    mutex_fraction: 0    # runtime.SetMutexProfileFraction; 0 is off
    block_rate: 0        # runtime.SetBlockProfileRate in nanoseconds; 0 is off
```

Every `interval` the exporter posts, to `<endpoint>/v1/pprof`, a CPU profile of that interval, then the heap profile (in use and allocated since start) and the goroutine profile. With `mutex_fraction` or `block_rate` above 0 it also sets that runtime rate and posts the mutex or block profile; both add overhead to every contended lock or blocking call, so leave them at 0 unless you are chasing contention. CPU profiling samples at 100 Hz for as long as profiles are on. On shutdown it posts the CPU profile of the last, partial interval.

A missing `endpoint`, an `endpoint` that is not an http(s) URL, a non-positive `interval`, or a negative `mutex_fraction` or `block_rate` does not stop the exporter: it logs an error naming the key and runs with profiles off. A value of the wrong type, such as an `interval` that is not a duration, fails startup like any other config key, even with profiles off. A failed push is logged as an error and the next interval pushes again. The push has no authentication: run the Collector next to the exporter, in the same cluster, and send on from there. There is no `/debug/pprof/` endpoint.

A Collector that receives the profiles, names them and forwards them as OTLP, tested with `otel/opentelemetry-collector-contrib:0.165.0`. The `pprof` receiver is only in the contrib distribution. pprof carries no resource attributes, so the `resource` processor sets `service.name`:

```yaml
receivers:
  pprof:
    server:
      endpoint: 0.0.0.0:4040
processors:
  resource:
    attributes:
      - key: service.name
        value: github-activity-exporter
        action: upsert
exporters:
  debug:
    verbosity: basic
  otlp_grpc:
    endpoint: profiles-backend:4317   # for example Pyroscope; must accept OTLP profiles
    tls:
      insecure: true
service:
  pipelines:
    profiles:
      receivers: [pprof]
      processors: [resource]
      exporters: [otlp_grpc, debug]
```

Start the Collector with `--feature-gates=+service.profilesSupport`. The `debug` exporter logs `"resource profiles": 1, "sample records": N` for each profile received.

To get OTLP profiles without the exporter's help, run the [OpenTelemetry eBPF profiler](https://github.com/open-telemetry/opentelemetry-ebpf-profiler) (`otelcol-ebpf-profiler`) as a DaemonSet: it profiles every process on the node from outside, needs privileged access to the node, and reads Go pprof labels. The pprof push is replaced by an in-process OTLP profiles exporter once the OTel Go SDK ships a stable profiles API and the backends accept that OTLP profiles version; the `telemetry.profiles` toggle stays.
