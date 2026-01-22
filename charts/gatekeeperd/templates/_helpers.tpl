{{/*
Expand the name of the chart.
*/}}
{{- define "gatekeeperd.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "gatekeeperd.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "gatekeeperd.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "gatekeeperd.labels" -}}
helm.sh/chart: {{ include "gatekeeperd.chart" . }}
{{ include "gatekeeperd.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "gatekeeperd.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gatekeeperd.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Get the name of the secret to use
*/}}
{{- define "gatekeeperd.secretName" -}}
{{- if .Values.existingSecret }}
{{- .Values.existingSecret }}
{{- else }}
{{- include "gatekeeperd.fullname" . }}-secrets
{{- end }}
{{- end }}

{{/*
Build Redis URI from configuration
*/}}
{{- define "gatekeeperd.redisUri" -}}
{{- if .Values.redis.enabled }}
{{- $scheme := "redis" }}
{{- if .Values.redis.tls }}
{{- $scheme = "rediss" }}
{{- end }}
{{- if .Values.redis.bundled }}
{{- /* Bundled Valkey subchart - use service name */}}
{{- $host := printf "%s-valkey-master" (include "gatekeeperd.fullname" .) }}
{{- printf "%s://%s:6379" $scheme $host }}
{{- else }}
{{- /* External Redis/Valkey */}}
{{- $host := required "redis.host is required when redis.bundled=false" .Values.redis.host }}
{{- $port := .Values.redis.port | default 6379 }}
{{- if .Values.redis.password }}
{{- printf "%s://:%s@%s:%d" $scheme .Values.redis.password $host (int $port) }}
{{- else }}
{{- printf "%s://%s:%d" $scheme $host (int $port) }}
{{- end }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Check if any routes use relay tokens
*/}}
{{- define "gatekeeperd.hasRelayRoutes" -}}
{{- $hasRelay := false }}
{{- range .Values.routes }}
{{- if .relayTokenKey }}
{{- $hasRelay = true }}
{{- end }}
{{- end }}
{{- $hasRelay }}
{{- end }}

{{/*
Validate configuration: multi-replica with relay requires redis
*/}}
{{- define "gatekeeperd.validateConfig" -}}
{{- $hasRelay := include "gatekeeperd.hasRelayRoutes" . }}
{{- if and (gt (int .Values.replicaCount) 1) (eq $hasRelay "true") (not .Values.redis.enabled) }}
{{- fail "redis.enabled must be true when replicaCount > 1 and routes use relayTokenKey (multi-replica relay requires Redis/Valkey)" }}
{{- end }}
{{- end }}
