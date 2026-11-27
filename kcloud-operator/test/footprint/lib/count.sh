#!/usr/bin/env bash
# ============================================================
# count.sh: 렌더된 매니페스트·라이브 파드에서 K8s 리소스 개수를 센다
# 상세: 정적(kind 별 개수) 집계와 라이브(파드/요청량/사용량) 집계 함수를 한곳에 모은다.
#       세는 단위를 한곳에 두어 대상마다 다르게 세는 일을 막는다.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
# shellcheck shell=bash

# require_cmd <명령…> — 없는 명령이 있으면 이름을 찍고 1 로 종료.
require_cmd() {
  local miss=0 c
  for c in "$@"; do
    command -v "$c" >/dev/null 2>&1 || { echo "필요한 명령 없음: $c" >&2; miss=1; }
  done
  return "$miss"
}

# yaml_docs <경로> — 다중 문서 YAML 을 JSON 배열 하나로. snap yq 가 /tmp 를 못 읽어
# python 으로 간다. 매핑이 아닌 문서(빈 문서 등)는 버린다.
yaml_docs() {
  python3 -c 'import sys,yaml,json; json.dump([d for d in yaml.safe_load_all(open(sys.argv[1])) if isinstance(d,dict)], sys.stdout)' "$1"
}

# kind_counts <렌더된 yaml 경로> — kind 별 개수와 워크로드 컨테이너 수를 JSON 한 줄로.
# 컨테이너 수는 Deployment/DaemonSet/StatefulSet/Job 의 spec.template.spec.containers 합.
kind_counts() {
  local f="$1"
  [[ -r "$f" ]] || { echo "읽을 수 없음: $f" >&2; return 1; }
  yaml_docs "$f" | jq -c '
    [ .[] | select(.kind != null) ] as $docs |
    { Deployment:  ($docs|map(select(.kind=="Deployment"))|length),
      DaemonSet:   ($docs|map(select(.kind=="DaemonSet"))|length),
      StatefulSet: ($docs|map(select(.kind=="StatefulSet"))|length),
      Job:         ($docs|map(select(.kind=="Job"))|length),
      CRD:         ($docs|map(select(.kind=="CustomResourceDefinition"))|length),
      ClusterRole: ($docs|map(select(.kind=="ClusterRole"))|length),
      Role:        ($docs|map(select(.kind=="Role"))|length),
      Webhook:     ($docs|map(select(.kind|test("WebhookConfiguration")))|length),
      Service:     ($docs|map(select(.kind=="Service"))|length),
      containers:  ([$docs[]|select(.kind|IN("Deployment","DaemonSet","StatefulSet","Job"))
                      |.spec.template.spec.containers|length]|add // 0) }'
}

# median <숫자…> — 중앙값. 값이 없으면 빈 문자열. 정수로 떨어지면 정수로 편다.
# 평균 대신 중앙값을 쓰는 이유: 기동 직후 한 번의 폭주가 평균을 통째로 끌고 간다.
median() {
  [[ $# -eq 0 ]] && { echo ""; return 0; }
  printf '%s\n' "$@" | sort -g | awk '
    function fmt(v){ if (v==int(v)) printf "%.0f\n", v; else printf "%g\n", v }
    { v[NR]=$1 }
    END { if (NR%2) fmt(v[(NR+1)/2]+0); else fmt((v[NR/2]+v[NR/2+1])/2) }'
}

# pod_requests <이름정규식> <ns…> — 이름이 정규식에 맞는 파드의 개수·컨테이너 수·요청량
# 합을 JSON 으로. cpu 는 milli, memory 는 Mi 로 환산. 요청 미지정 컨테이너는 0 으로 더한다.
pod_requests() {
  local rx="$1"; shift
  local ns merged='{"items":[]}' j tmp
  for ns in "$@"; do
    j="$(kubectl get pods -n "$ns" -o json 2>/dev/null)" || continue
    merged="$(jq -c --argjson m "$merged" '{items: ($m.items + .items)}' <<<"$j")"
  done
  tmp="$(mktemp)"
  printf '%s' "$merged" >"$tmp"
  # python script 는 stdin(heredoc), 데이터는 argv 파일로 넘긴다(둘 다 stdin 이면 충돌).
  python3 - "$rx" "$tmp" <<'PY'
import sys, json, re
rx = re.compile(sys.argv[1])
data = json.load(open(sys.argv[2]))
def cpu_milli(s):
    if not s: return 0.0
    s = str(s)
    if s.endswith('m'): return float(s[:-1])
    if s.endswith('u'): return float(s[:-1]) / 1e3
    if s.endswith('n'): return float(s[:-1]) / 1e6
    return float(s) * 1000
def mem_mi(s):
    if not s: return 0.0
    s = str(s)
    units = {'Ki': 1024, 'Mi': 1024**2, 'Gi': 1024**3, 'Ti': 1024**4,
             'K': 1e3, 'M': 1e6, 'G': 1e9, 'T': 1e12}
    for u, mult in units.items():
        if s.endswith(u): return float(s[:-len(u)]) * mult / (1024**2)
    return float(s) / (1024**2)
pods = containers = 0
cpu = mem = 0.0
for p in data.get('items', []):
    name = p.get('metadata', {}).get('name', '')
    if not rx.search(name): continue
    pods += 1
    for c in p.get('spec', {}).get('containers', []):
        containers += 1
        req = (c.get('resources') or {}).get('requests') or {}
        cpu += cpu_milli(req.get('cpu', ''))
        mem += mem_mi(req.get('memory', ''))
print(json.dumps({"pods": pods, "containers": containers,
                  "cpuRequestMilli": round(cpu), "memRequestMi": round(mem)}))
PY
  rm -f "$tmp"
}

# ds_count <이름정규식> <ns…> — 이름이 정규식에 맞는 DaemonSet 개수 합.
ds_count() {
  local rx="$1"; shift
  local ns n total=0
  for ns in "$@"; do
    n="$(kubectl get daemonsets -n "$ns" -o json 2>/dev/null \
          | jq --arg rx "$rx" '[.items[]|select(.metadata.name|test($rx))]|length')" || n=0
    total=$((total + n))
  done
  echo "$total"
}

# top_used <이름정규식> <표본수> <간격초> <ns…> — N 회 표본의 총 사용량(cpu milli / mem Mi)
# 중앙값을 "cpu mem" 두 값으로. metrics-server 미설치 등으로 표본이 없으면 "null null".
top_used() {
  local rx="$1" samples="$2" interval="$3"; shift 3
  local -a nslist=("$@")
  local -a cpus=() mems=()
  local i ns out got c m cc mm
  for (( i=0; i<samples; i++ )); do
    c=0; m=0; got=0
    for ns in "${nslist[@]}"; do
      out="$(kubectl top pods -n "$ns" --containers --no-headers 2>/dev/null)" || continue
      [[ -n "$out" ]] || continue
      # 메모리는 kubectl top 이 Mi 로 내는 것이 보통이나 Gi/Ki 로 낼 수도 있다. 단위를
      # 벗기지 않고 더하면 awk 가 "1Gi" 를 1 로 읽어 조용히 오독하므로 Mi 로 환산해 더한다.
      read -r cc mm <<<"$(awk -v rx="$rx" \
        '$1 ~ rx {
           gsub(/m$/,"",$3); cs+=$3
           mv=$4
           if      (mv ~ /Gi$/) { sub(/Gi$/,"",mv); mv=mv*1024 }
           else if (mv ~ /Mi$/) { sub(/Mi$/,"",mv) }
           else if (mv ~ /Ki$/) { sub(/Ki$/,"",mv); mv=mv/1024 }
           ms+=mv
         }
         END { printf "%d %d", cs, ms }' <<<"$out")"
      c=$((c + cc)); m=$((m + mm)); got=1
    done
    [[ "$got" -eq 1 ]] && { cpus+=("$c"); mems+=("$m"); }
    if (( i < samples - 1 )); then sleep "$interval"; fi
  done
  if [[ "${#cpus[@]}" -eq 0 ]]; then echo "null null"; return 0; fi
  echo "$(median "${cpus[@]}") $(median "${mems[@]}")"
}

# discover_prometheus — 질의할 Prometheus 서버를 "ns svc port" 한 줄로 찾는다. 없으면
# 빈 문자열. PROM_URL 이 있으면 그대로 "env:$PROM_URL" 을 돌려준다(prom_used 가 구분).
# label app.kubernetes.io/name=prometheus 를 먼저 본다 — kube-prometheus-stack 의
# kubelet 프록시 Service(...-kube-prome-kubelet) 는 이 레이블을 달지 않는다. 레이블이
# 없는 배포를 위해 이름 패턴("prometheus" 포함, kubelet/alertmanager/pushgateway/
# operator 제외)으로 한 번 더 찾는다.
discover_prometheus() {
  if [[ -n "${PROM_URL:-}" ]]; then echo "env:$PROM_URL"; return 0; fi
  local line
  line="$(kubectl get svc -A -l app.kubernetes.io/name=prometheus -o json 2>/dev/null \
        | jq -r '.items[0] | select(.!=null) | "\(.metadata.namespace) \(.metadata.name) \(.spec.ports[0].port)"')"
  if [[ -z "$line" ]]; then
    line="$(kubectl get svc -A -o json 2>/dev/null | jq -r '
      [.items[] | select(.metadata.name|test("prometheus";"i"))
                 | select(.metadata.name|test("kubelet|alertmanager|pushgateway|operator";"i")|not)]
      | .[0] | select(.!=null) | "\(.metadata.namespace) \(.metadata.name) \(.spec.ports[0].port)"')"
  fi
  echo "$line"
}

# prom_used <discover_prometheus 출력> <이름정규식> — PromQL range query 로 10분 창
# (30초 스텝)의 CPU(milli)/메모리(Mi) 중앙값을 "cpu mem" 두 값으로. 대상 population 은
# top_used 와 맞춘다(같은 이름정규식·같은 네임스페이스 셋). "env:URL" 이면 그 URL 을
# 바로 쓰고, 아니면 "ns svc port" 를 포트포워드해 붙는다. 실패/무응답이면 "null null".
prom_used() {
  local target="$1" rx="$2"
  local base ns svc port lport
  # PF_PID(전역, count.sh 를 source 한 스크립트와 공유) — set -e 로 이 함수가 아래
  # kill 줄에 못 닿고 중단되거나 스크립트가 신호로 죽어도, 호출자의 EXIT trap 이
  # PF_PID 를 보고 포트포워드를 마저 종료한다(measure.sh 의 TMPD trap 참고).
  PF_PID=""
  if [[ "$target" == env:* ]]; then
    base="${target#env:}"
  else
    read -r ns svc port <<<"$target"
    [[ -z "$svc" ]] && { echo "null null"; return 0; }
    lport=$(( (RANDOM % 20000) + 20000 ))
    kubectl -n "$ns" port-forward "svc/$svc" "$lport:$port" >/dev/null 2>&1 &
    PF_PID=$!
    local i ready=0
    for (( i=0; i<25; i++ )); do
      curl -fsS -m 1 "http://127.0.0.1:$lport/-/ready" >/dev/null 2>&1 && { ready=1; break; }
      sleep 0.2
    done
    if [[ "$ready" -eq 0 ]]; then
      kill "$PF_PID" 2>/dev/null; wait "$PF_PID" 2>/dev/null; PF_PID=""
      echo "null null"; return 0
    fi
    base="http://127.0.0.1:$lport"
  fi
  local end start ns_alt cpu_q mem_q cpu_out mem_out
  end="$(date +%s)"; start="$((end - 600))"
  # 대상 네임스페이스는 top_used 와 같은 셋(kcloud|kube-system|rbln-system), 파드는
  # 같은 이름정규식으로 걸러 두 경로의 population 을 맞춘다.
  ns_alt='kcloud|kube-system|rbln-system'
  cpu_q="sum(rate(container_cpu_usage_seconds_total{namespace=~\"^(${ns_alt})\$\", pod=~\"${rx}.*\", container!=\"\", container!=\"POD\"}[5m])) * 1000"
  mem_q="sum(container_memory_working_set_bytes{namespace=~\"^(${ns_alt})\$\", pod=~\"${rx}.*\", container!=\"\", container!=\"POD\"}) / 1048576"
  cpu_out="$(curl -fsS -m 10 --data-urlencode "query=$cpu_q" --data-urlencode "start=$start" \
        --data-urlencode "end=$end" --data-urlencode "step=30" "$base/api/v1/query_range" 2>/dev/null)"
  mem_out="$(curl -fsS -m 10 --data-urlencode "query=$mem_q" --data-urlencode "start=$start" \
        --data-urlencode "end=$end" --data-urlencode "step=30" "$base/api/v1/query_range" 2>/dev/null)"
  [[ -n "$PF_PID" ]] && { kill "$PF_PID" 2>/dev/null; wait "$PF_PID" 2>/dev/null; PF_PID=""; }
  local -a cvl mvl
  mapfile -t cvl < <(jq -r '.data.result[0].values[]?[1] // empty' <<<"$cpu_out" 2>/dev/null | awk '{printf "%.0f\n", $1}')
  mapfile -t mvl < <(jq -r '.data.result[0].values[]?[1] // empty' <<<"$mem_out" 2>/dev/null | awk '{printf "%.0f\n", $1}')
  if [[ "${#cvl[@]}" -eq 0 || "${#mvl[@]}" -eq 0 ]]; then echo "null null"; return 0; fi
  echo "$(median "${cvl[@]}") $(median "${mvl[@]}")"
}
