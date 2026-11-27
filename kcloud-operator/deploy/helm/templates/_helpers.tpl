{{/*
Expand the name of the chart.
*/}}
{{- define "kcloud-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "kcloud-operator.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name "controller-manager" | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/*
ServiceAccount name. create 가 참이면(기본) 이름이 없을 때 fullname 으로 떨어진다 —
rbac.yaml 이 같은 이름으로 ServiceAccount 를 만들기 때문이다. create 가 거짓인데
이름도 비어 있으면 네임스페이스의 기본 "default" SA 로 떨어진다(차트 이름을
추측하지 않는다).
*/}}
{{- define "kcloud-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{ default (include "kcloud-operator.fullname" .) .Values.serviceAccount.name }}
{{- else -}}
{{ default "default" .Values.serviceAccount.name }}
{{- end -}}
{{- end -}}

{{/*
Common selector labels
*/}}
{{- define "kcloud-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kcloud-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Assemble a fully-qualified image string. Input dict keys: registry(may be empty), repo, tag.
registry 가 있으면 "<registry>/<repo>:<tag>", 없으면 "<repo>:<tag>"(registry-relative).
기본값에 레지스트리를 두지 않아 install 시 global.registry 로 한 줄 주입할 수 있다.
*/}}
{{- define "kcloud-operator.image" -}}
{{- $reg := .registry | default "" -}}
{{- if $reg -}}{{ $reg }}/{{ .repo }}:{{ .tag }}{{- else -}}{{ .repo }}:{{ .tag }}{{- end -}}
{{- end -}}

{{/*
Assemble a 3rd-party(vendor) image string. dict keys: registry(global.vendorRegistry,
may be empty), upstream(vendor host), repo("<org>/<image>"), tag.
registry 가 비면 "<upstream>/<repo>:<tag>"(공개 레지스트리), 채워지면 "<registry>/<repo>:<tag>"
(air-gap 미러, org 경로 유지). kcloud 빌드 이미지는 kcloud-operator.image+global.registry 사용.
*/}}
{{- define "kcloud-operator.vendorImage" -}}
{{- $reg := .registry | default .upstream | default "" -}}
{{- include "kcloud-operator.image" (dict "registry" $reg "repo" .repo "tag" .tag) -}}
{{- end -}}
