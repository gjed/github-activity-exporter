# Metric naming contract (https://github.com/gjed/github-activity-exporter/issues/57).
#
# Every metric declares its Prometheus exposure as annotations.prometheus:
# name equals the metric name (translation strategy NoTranslation) and type
# follows from the instrument. Run by:
#   weaver registry check --v2 -r registry/ -p policies/registry/
package after_resolution

import rego.v1

# Prometheus type each OTel instrument is exposed as.
prometheus_type := {
	"counter": "counter",
	"updowncounter": "gauge",
	"gauge": "gauge",
	"histogram": "histogram",
}

deny contains finding("prometheus_annotation_missing", m, msg) if {
	some m in input.registry.metrics
	some key in ["name", "type"]
	not m.annotations.prometheus[key]
	msg := sprintf("Metric '%s' does not declare annotations.prometheus.%s.", [m.name, key])
}

deny contains finding("prometheus_name_mismatch", m, msg) if {
	some m in input.registry.metrics
	name := m.annotations.prometheus.name
	name != m.name
	msg := sprintf("Metric '%s' declares annotations.prometheus.name '%v'; it must equal the metric name.", [m.name, name])
}

deny contains finding("prometheus_type_mismatch", m, msg) if {
	some m in input.registry.metrics
	type := m.annotations.prometheus.type
	type != prometheus_type[m.instrument]
	msg := sprintf("Metric '%s' (%s) declares annotations.prometheus.type '%v'; want '%s'.", [m.name, m.instrument, type, prometheus_type[m.instrument]])
}

finding(id, m, msg) := {
	"id": id,
	"level": "violation",
	"signal_type": "metric",
	"signal_name": m.name,
	"context": {"metric_name": m.name},
	"message": msg,
}
