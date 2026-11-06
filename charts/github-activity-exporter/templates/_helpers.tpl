{{- define "github-activity-exporter.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "github-activity-exporter.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "github-activity-exporter.selectorLabels" -}}
app.kubernetes.io/name: {{ include "github-activity-exporter.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "github-activity-exporter.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "github-activity-exporter.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "github-activity-exporter.secretName" -}}
{{- .Values.secret.existingSecret | default (include "github-activity-exporter.fullname" .) }}
{{- end }}

{{/* Whether the config enables the Prometheus bridge. */}}
{{- define "github-activity-exporter.bridgeEnabled" -}}
{{- if dig "export" "prometheus" "enabled" false .Values.config }}true{{ end }}
{{- end }}

{{/*
Fail on values that cannot work together. The config is only read here,
never written: see values.yaml `config`.
*/}}
{{- define "github-activity-exporter.validate" -}}
{{- $s := .Values.secret }}
{{- if and $s.create $s.existingSecret }}
{{- fail "secret.create and secret.existingSecret are mutually exclusive" }}
{{- end }}
{{- if and $s.create (empty $s.data) }}
{{- fail "secret.create is true but secret.data is empty" }}
{{- end }}
{{- if and (or $s.env $s.files) (not (or $s.create $s.existingSecret)) }}
{{- fail "secret.env and secret.files need secret.existingSecret or secret.create" }}
{{- end }}
{{- $health := dig "health" "listen_address" "" .Values.config | toString }}
{{- if and $health (not (hasSuffix (printf ":%v" .Values.health.port) $health)) }}
{{- fail (printf "config.health.listen_address %q does not use health.port %v" $health .Values.health.port) }}
{{- end }}
{{- $bridge := include "github-activity-exporter.bridgeEnabled" . }}
{{- $metrics := dig "export" "prometheus" "listen_address" "" .Values.config | toString }}
{{- if and $bridge $metrics (not (hasSuffix (printf ":%v" .Values.metrics.port) $metrics)) }}
{{- fail (printf "config.export.prometheus.listen_address %q does not use metrics.port %v" $metrics .Values.metrics.port) }}
{{- end }}
{{- if and .Values.metrics.service.enabled (not $bridge) }}
{{- fail "metrics.service.enabled needs config.export.prometheus.enabled: true" }}
{{- end }}
{{- if and .Values.metrics.serviceMonitor.enabled (not .Values.metrics.service.enabled) }}
{{- fail "metrics.serviceMonitor.enabled needs metrics.service.enabled: true" }}
{{- end }}
{{- end }}
