{{/*
Expand the name of the chart.
*/}}
{{- define "npu-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "npu-operator.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name "controller-manager" | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
ServiceAccount name.
*/}}
{{- define "npu-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.name -}}
{{ .Values.serviceAccount.name }}
{{- else -}}
{{ include "npu-operator.fullname" . }}
{{- end -}}
{{- end -}}

{{/*
Common selector labels
*/}}
{{- define "npu-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "npu-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Assemble a fully-qualified image string from a registry-relative repo + tag.
Input dict keys: registry (may be empty), repo, tag.
- registry 가 비어있지 않으면 "<registry>/<repo>:<tag>" 로 prefix.
- 비어있으면 "<repo>:<tag>" (registry-relative, e.g. Docker Hub 기본).
사설 IP 를 values 기본값에서 제거하고 install 시 global.registry 로 1줄 주입하기 위함.
*/}}
{{- define "kcloud-operator.image" -}}
{{- $reg := .registry | default "" -}}
{{- if $reg -}}{{ $reg }}/{{ .repo }}:{{ .tag }}{{- else -}}{{ .repo }}:{{ .tag }}{{- end -}}
{{- end -}}
