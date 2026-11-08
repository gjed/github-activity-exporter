# Checks the type a /metrics family is exposed as against the metric's
# annotations.prometheus.type (https://github.com/gjed/github-activity-exporter/issues/57).
#
# Used by the /metrics live-check in tests/integration, whose samples carry the
# exposed Prometheus type (counter, gauge, histogram) as the metric instrument.
# A name that is not in the registry, such as github.pr.automerged_total, gets
# weaver's built-in missing_metric violation.
package live_check_advice

import rego.v1

# Prometheus type each OTel instrument is exposed as (same table as
# policies/registry/prometheus.rego).
prometheus_type := {
	"counter": "counter",
	"updowncounter": "gauge",
	"gauge": "gauge",
	"histogram": "histogram",
}

# A metric imported from the OTel semantic conventions declares its
# annotations.prometheus on a refinement, but live-check matches the sample to
# the imported base metric, which has none. For those the expected type is the
# one derived from the instrument: policies/registry/ already requires the
# refinement's annotations.prometheus.type to equal it.
expected_type := input.registry_group.annotations.prometheus.type

expected_type := prometheus_type[input.registry_group.instrument] if {
	not input.registry_group.annotations.prometheus.type
}

deny contains finding if {
	metric := input.sample.metric
	want := expected_type
	metric.instrument != want
	finding := {
		"id": "prometheus_type_mismatch",
		"level": "violation",
		"signal_type": "metric",
		"signal_name": metric.name,
		"context": {"expected": want, "found": metric.instrument},
		"message": sprintf("Metric '%s' is exposed as Prometheus type '%s'; annotations.prometheus.type is '%s'.", [metric.name, metric.instrument, want]),
	}
}
