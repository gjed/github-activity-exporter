# Checks the type a /metrics family is exposed as against the metric's
# annotations.prometheus.type (https://github.com/gjed/github-activity-exporter/issues/57).
#
# Used by the /metrics live-check in tests/integration, whose samples carry the
# exposed Prometheus type (counter, gauge, histogram) as the metric instrument.
# A name that is not in the registry, such as github.pr.automerged_total, gets
# weaver's built-in missing_metric violation.
package live_check_advice

import rego.v1

deny contains finding if {
	metric := input.sample.metric
	want := input.registry_group.annotations.prometheus.type
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
