{{/*
Chart name, overridable.
*/}}
{{- define "tutti-frutti.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Base fully-qualified app name (release-aware, overridable).
*/}}
{{- define "tutti-frutti.fullname" -}}
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

{{/*
Fully-qualified name for a component (leaves room for the suffix inside 63 chars).
Usage: include "tutti-frutti.componentFullname" (dict "component" "server" "context" .)
*/}}
{{- define "tutti-frutti.componentFullname" -}}
{{- printf "%s-%s" (include "tutti-frutti.fullname" .context | trunc 56 | trimSuffix "-") .component }}
{{- end }}

{{/*
Chart label value.
*/}}
{{- define "tutti-frutti.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels (no component), used by shared resources such as the Ingress.
*/}}
{{- define "tutti-frutti.labels" -}}
helm.sh/chart: {{ include "tutti-frutti.chart" . }}
app.kubernetes.io/name: {{ include "tutti-frutti.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: tutti-frutti
{{- end }}

{{/*
Selector labels for a component (immutable once deployed: keep this set small).
Usage: include "tutti-frutti.selectorLabels" (dict "component" "server" "context" .)
*/}}
{{- define "tutti-frutti.selectorLabels" -}}
app.kubernetes.io/name: {{ include "tutti-frutti.name" .context }}
app.kubernetes.io/instance: {{ .context.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Full labels for a component.
Usage: include "tutti-frutti.componentLabels" (dict "component" "server" "context" .)
*/}}
{{- define "tutti-frutti.componentLabels" -}}
helm.sh/chart: {{ include "tutti-frutti.chart" .context }}
{{ include "tutti-frutti.selectorLabels" . }}
{{- if .context.Chart.AppVersion }}
app.kubernetes.io/version: {{ .context.Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .context.Release.Service }}
app.kubernetes.io/part-of: tutti-frutti
{{- end }}

{{/*
Public URL of the app: explicit override, otherwise derived from the ingress host.
*/}}
{{- define "tutti-frutti.publicUrl" -}}
{{- if .Values.server.publicUrl }}
{{- .Values.server.publicUrl }}
{{- else }}
{{- printf "%s://%s" (ternary "https" "http" .Values.ingress.tls.enabled) .Values.ingress.host }}
{{- end }}
{{- end }}
