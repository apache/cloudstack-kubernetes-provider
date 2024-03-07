{{/*
Expand the name of the chart.
*/}}
{{- define "ccm.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "ccm.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels and app labels
*/}}
{{- define "ccm.labels" -}}
app.kubernetes.io/name: {{ include "ccm.name" . }}
helm.sh/chart: {{ include "ccm.chart" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "ccm.common.matchLabels" -}}
app: {{ template "ccm.name" . }}
release: {{ .Release.Name }}
{{- end -}}

{{- define "ccm.common.metaLabels" -}}
chart: {{ template "ccm.chart" . }}
heritage: {{ .Release.Service }}
{{- end -}}

{{- define "ccm.controllermanager.matchLabels" -}}
component: controllermanager
{{ include "ccm.common.matchLabels" . }}
{{- end -}}

{{- define "ccm.controllermanager.labels" -}}
{{ include "ccm.controllermanager.matchLabels" . }}
{{ include "ccm.common.metaLabels" . }}
{{- end -}}

{{/*
Create cloud-config makro.
*/}}
{{- define "cloudConfig" -}}
[Global]
{{- range $key, $value := .Values.cloudConfig.global }}
{{ $key }} = {{ $value | quote }}
{{- end }}
{{- end -}}

{{/*
Generate string of enabled controllers. Might have a trailing comma (,) which needs to be trimmed.
*/}}
{{- define "ccm.enabledControllers" }}
{{- range .Values.enabledControllers -}}{{ . }},{{- end -}}
{{- end }}