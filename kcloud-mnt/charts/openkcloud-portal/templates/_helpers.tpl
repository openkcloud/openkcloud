{{/* 공통 라벨 */}}
{{- define "okc.labels" -}}
app.kubernetes.io/part-of: openkcloud-portal
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{/* 시크릿 값 — values 에 적힌 값, 비어 있으면 클러스터에 있는 기존 값(lookup). 사용: include "okc.secretValue" (list <values 값> <기존 data> <키>) */}}
{{- define "okc.secretValue" -}}
{{- $v := toString (index . 0 | default "") -}}
{{- if $v -}}{{ $v }}{{- else -}}{{ index (index . 1) (index . 2) | default "" | b64dec }}{{- end -}}
{{- end -}}

{{/* 앱 시크릿 — 빈 값 키는 넣지 않는다. 릴리스와 함께 만들고 지운다(helm uninstall 하면 삭제). 사용: include "okc.secret" (list $root <이름> <키→값 dict>) */}}
{{- define "okc.secret" -}}
{{- $root := index . 0 -}}
apiVersion: v1
kind: Secret
metadata:
  name: {{ index . 1 }}
  namespace: {{ $root.Values.global.namespace }}
  labels:
    {{- include "okc.labels" $root | nindent 4 }}
type: Opaque
data:
{{- range $k, $v := index . 2 }}
{{- if $v }}
  {{ $k }}: {{ $v | b64enc }}
{{- end }}
{{- end }}
{{- end -}}

{{/* 워커 전용 affinity (global.nodeSelectorWorkerOnly) */}}
{{- define "okc.workerAffinity" -}}
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: node-role.kubernetes.io/control-plane
              operator: DoesNotExist
{{- end -}}
