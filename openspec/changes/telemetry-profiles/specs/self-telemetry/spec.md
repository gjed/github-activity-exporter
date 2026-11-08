## MODIFIED Requirements

### Requirement: Telemetry configuration section

The config file SHALL accept a `telemetry` section with `enabled` (default `false`), `metrics.enabled`, `traces.enabled`, `logs.enabled` (each default `true`) and `profiles.enabled` (default `false`). Each key SHALL be overridable by a `GITHUB_ACTIVITY_EXPORTER_` env var, upper-cased with `.` replaced by `_`.

#### Scenario: Defaults

- **WHEN** the config has no `telemetry` section
- **THEN** `telemetry.enabled` is `false`, `metrics.enabled`, `traces.enabled` and `logs.enabled` are `true`, and `profiles.enabled` is `false`

#### Scenario: Env override

- **WHEN** `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_ENABLED=true` and `GITHUB_ACTIVITY_EXPORTER_TELEMETRY_LOGS_ENABLED=false` are set and the file sets neither
- **THEN** `telemetry.enabled` is `true` and `telemetry.logs.enabled` is `false`

#### Scenario: Invalid value is fatal

- **WHEN** a `telemetry` toggle is set, in the file or by env var, to a value that is not a boolean
- **THEN** the exporter logs an error naming the key and exits with a non-zero status before collecting anything

## ADDED Requirements

### Requirement: Profiles push to an OpenTelemetry Collector

When the profiles signal is active, the exporter SHALL push gzip-compressed pprof profiles of its own process with `POST <telemetry.profiles.endpoint>/v1/pprof`, the push API of the OpenTelemetry Collector `pprof` receiver: every `telemetry.profiles.interval` (default `60s`) a CPU profile covering that interval, then heap and goroutine profiles.

#### Scenario: Profiles reach the Collector

- **WHEN** `telemetry.enabled` and `telemetry.profiles.enabled` are true, `telemetry.profiles.endpoint` points at a Collector `pprof` receiver, and the exporter runs for longer than one interval
- **THEN** the Collector receives CPU, heap and goroutine profiles

#### Scenario: Signal inactive

- **WHEN** `telemetry.enabled` is false, `telemetry.profiles.enabled` is false, or `OTEL_SDK_DISABLED=true`
- **THEN** no profile is collected and no connection is opened to `telemetry.profiles.endpoint`

#### Scenario: No pull endpoint

- **WHEN** the profiles signal is active
- **THEN** the health server answers `/debug/pprof/` with 404

#### Scenario: Failed push

- **WHEN** a push fails or the Collector answers with a non-2xx status
- **THEN** the exporter logs an error with the profile type and the cause, and pushes again at the next interval

### Requirement: Profiles configuration errors are not fatal

When `telemetry.enabled` and `telemetry.profiles.enabled` are true and `endpoint` is not an http(s) URL with a host, `interval` is not a positive duration, or `mutex_fraction` or `block_rate` is negative, the exporter SHALL log an error naming the key, keep the profiles signal inactive, and start normally.

#### Scenario: Missing endpoint

- **WHEN** `telemetry.enabled` and `telemetry.profiles.enabled` are true and `telemetry.profiles.endpoint` is empty
- **THEN** the exporter logs an error naming `telemetry.profiles.endpoint`, does not push profiles, and collects and exports GitHub metrics as usual

#### Scenario: Invalid rate

- **WHEN** the profiles signal is enabled and `mutex_fraction` is `-1`
- **THEN** the exporter logs an error naming `telemetry.profiles.mutex_fraction` and does not push profiles

### Requirement: Mutex and block profiling rates

`telemetry.profiles.mutex_fraction` and `telemetry.profiles.block_rate` SHALL default to `0` (off). When the profiles signal is active and a value is above `0`, the exporter SHALL set the Go runtime mutex profile fraction or block profile rate to it and push the mutex or block profile every interval; otherwise it SHALL leave the runtime rates at `0` and not push them.

#### Scenario: Off by default

- **WHEN** the profiles signal is active and neither key is set
- **THEN** no mutex or block profile is pushed and the runtime mutex fraction and block rate stay `0`

#### Scenario: Enabled

- **WHEN** the profiles signal is active, `mutex_fraction` is `100` and `block_rate` is `100000`
- **THEN** mutex and block profiles are pushed every interval with those runtime rates

#### Scenario: Ignored when inactive

- **WHEN** `mutex_fraction` is `100` and the profiles signal is inactive
- **THEN** the runtime mutex fraction stays `0`

### Requirement: Profiles shutdown

On SIGTERM or SIGINT, within the existing shutdown deadline, the exporter SHALL stop the profiler and push the CPU profile of the current, partial interval.

#### Scenario: Last window pushed

- **WHEN** the profiles signal is active and the exporter receives SIGTERM halfway through an interval
- **THEN** the CPU profile of that half interval is pushed and the process exits within the shutdown deadline
