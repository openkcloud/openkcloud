#!/usr/bin/env bash
# ============================================================
# run.sh: NVIDIA ACPP 시분할 적용·해제로 kcloud_acpp_apply_total 라이브 표본을 남긴다(<nvidia-node>)
# 상세: acpp-timesliced.yaml 을 적용해 result="ready", 같은 객체를 exclusive 로 patch 해
#       result="disabled" 를 kcloud_acpp_apply_total{vendor="nvidia",...} 로 찍는다.
#       port-forward+curl 로 스크레이프하며, 지표 엔드포인트는 helm upgrade 로 켰다가 끝에
#       원복한다(세부는 cleanup()·demo 섹션 참고). DRY_RUN=1 은 mutating 명령만 건너뛴다.
# 생성일: 2026-09-23 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

DRY_RUN="${DRY_RUN:-0}"
KUBECTL_CONTEXT="${KUBECTL_CONTEXT:-kubernetes-admin@cluster.local}"

NAMESPACE="${NAMESPACE:-kcloud}"
RELEASE="${RELEASE:-kcloud-operator}"
CHART_DIR="${CHART_DIR:-$REPO_ROOT/deploy/helm}"
NODE="${NODE:-k8s-worker1}"

ACPP_FILE="${ACPP_FILE:-$SCRIPT_DIR/acpp-timesliced.yaml}"
ACPP_NAME="${ACPP_NAME:-nvidia-timesliced-metric-probe}"
VENDOR="${VENDOR:-nvidia}"
MODE_SHARED="${MODE_SHARED:-timeSliced}"
MODE_EXCLUSIVE="${MODE_EXCLUSIVE:-exclusive}"

DP_NS="${DP_NS:-kube-system}"
DP_LABEL="${DP_LABEL:-kcloud.ai/dp-vendor=nvidia}"
RESOURCE="${RESOURCE:-nvidia.com/gpu}"
RESOURCE_JP="${RESOURCE//./\\.}"

METRIC="${METRIC:-kcloud_acpp_apply_total}"
LOCAL_PORT="${LOCAL_PORT:-18083}"
METRICS_PORT="${METRICS_PORT:-8080}"

# 대기 상한(초)·간격.
T_READY="${T_READY:-600}"       # apply → status.phase=Ready (브리핑: 최대 10분)
T_DISABLED="${T_DISABLED:-300}" # exclusive patch → status.phase=Failed
T_RESTORE="${T_RESTORE:-300}"   # delete → device-plugin Running + allocatable 원복
HELM_TIMEOUT="${HELM_TIMEOUT:-5m}"
IV="${IV:-10}"

OUT="${OUT:-/tmp/nvidia-acpp-metrics-evidence}"
mkdir -p "$OUT"
VALUES_BASE="${VALUES_BASE:-$OUT/values-baseline.yaml}"

export KUBECTL_CONTEXT NAMESPACE NODE RESOURCE

BASE_ALLOC=""
DEPLOYED_TAG=""
METRICS_ENABLED=false
ACPP_APPLIED=false
PF_PID=""
CLEANED=false
BLOCKED=false
DEMO_DONE=false
CHECK_JSON=""
FAILS=0
PHASE="init"

utc_now() { date -u +%FT%TZ; }
log() { printf '%s\n' "$*" >&2; }

record() {
  local name="$1" ok="$2" detail="${3:-}"
  if [ "$ok" != "true" ]; then FAILS=$((FAILS + 1)); fi
  CHECK_JSON="${CHECK_JSON:+$CHECK_JSON,}\"$name\":$ok"
  local tag; tag=$([ "$ok" = "true" ] && echo PASS || echo FAIL)
  log "  [$tag] $name ${detail:+— $detail}"
}

# kc: ACPP 를 만들고/바꾸고/지우는 kubectl 호출 전용 통로. DRY_RUN 은 출력만.
kc() {
  if [ "$DRY_RUN" = "1" ]; then printf '+ kubectl --context %s %s\n' "$KUBECTL_CONTEXT" "$*" >&2; return 0; fi
  kubectl --context "$KUBECTL_CONTEXT" "$@"
}
# kubeget: 읽기 전용 조회 — DRY_RUN 과 무관하게 항상 실제로 실행한다.
kubeget() { kubectl --context "$KUBECTL_CONTEXT" "$@"; }
helmx() { helm --kube-context "$KUBECTL_CONTEXT" "$@"; }
# run_or_print: helm 처럼 kc() 통로를 안 쓰는 mutating 명령의 단일 통로.
run_or_print() {
  if [ "$DRY_RUN" = "1" ]; then log "DRY: $*"; else log "+ $*"; "$@"; fi
}

poll() {
  local t="$1" iv="$2"; shift 2
  if [ "$DRY_RUN" = "1" ]; then printf '+ (poll ≤%ss) %s\n' "$t" "$*" >&2; return 0; fi
  local e=0
  while [ "$e" -lt "$t" ]; do
    if "$@"; then return 0; fi
    sleep "$iv"; e=$((e + iv))
  done
  return 1
}

operator_pod() { kubeget -n "$NAMESPACE" get pods -l app.kubernetes.io/name=kcloud-operator -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true; }
node_alloc() { kubeget get node "$NODE" -o jsonpath="{.status.allocatable.$RESOURCE_JP}" 2>/dev/null || echo ""; }
gpu_pods_on_node_count() {
  kubeget get pods -A -o json 2>/dev/null | python3 -c '
import json, os, sys
n = os.environ["NODE"]; r = os.environ["RESOURCE"]
d = json.load(sys.stdin)
c = 0
for p in d["items"]:
    if p["spec"].get("nodeName") != n:
        continue
    for cn in p["spec"]["containers"]:
        if r in (cn.get("resources", {}).get("requests", {}) or {}):
            c += 1
print(c)
'
}
acpp_phase() { kubeget get acpp "$ACPP_NAME" -o jsonpath='{.status.phase}' 2>/dev/null || echo ""; }
acpp_ready() { [ "$(acpp_phase)" = "Ready" ]; }
acpp_failed() { [ "$(acpp_phase)" = "Failed" ]; }
dp_all_running() {
  local out
  out="$(kubeget -n "$DP_NS" get pods -l "$DP_LABEL" --field-selector "spec.nodeName=$NODE" -o json 2>/dev/null)" || return 1
  printf '%s' "$out" | python3 -c '
import json, sys
d = json.load(sys.stdin)
items = d.get("items", [])
sys.exit(1 if not items else (0 if all(p["status"].get("phase") == "Running" for p in items) else 1))
'
}
alloc_restored() { local a; a="$(node_alloc)"; [ -n "$a" ] && [ "$a" = "$BASE_ALLOC" ]; }
restored_after_delete() { dp_all_running && alloc_restored; }

deployed_image_tag() {
  local t
  t="$(python3 -c '
import sys, yaml
try:
    d = yaml.safe_load(open(sys.argv[1])) or {}
except Exception:
    d = {}
print((d.get("image") or {}).get("tag", ""))
' "$VALUES_BASE" 2>/dev/null)"
  if [ -z "$t" ]; then
    t="$(kubeget -n "$NAMESPACE" get deploy "$RELEASE" -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null | sed -E 's/.*://')"
  fi
  printf '%s' "$t"
}

# metric_value FILE MODE RESULT — Prometheus 텍스트에서 vendor=$VENDOR,mode=MODE,result=RESULT
# 라인의 값(정수, 없으면 0)을 읽는다. 레이블 순서에 의존하지 않는다(client_golang 은 보통
# 알파벳순으로 내지만 파싱은 순서 무관하게 짠다).
metric_value() {
  local file="$1" mode="$2" result="$3"
  python3 -c '
import re, sys
path, metric, vendor, mode, result = sys.argv[1:6]
pat = re.compile(r"^" + re.escape(metric) + r"\{([^}]*)\}\s+([0-9.eE+-]+)\s*$")
want = {"vendor": vendor, "mode": mode, "result": result}
val = 0.0
try:
    with open(path) as f:
        for line in f:
            m = pat.match(line.strip())
            if not m:
                continue
            labels = dict(re.findall(r"(\w+)=\"([^\"]*)\"", m.group(1)))
            if all(labels.get(k) == v for k, v in want.items()):
                val = float(m.group(2))
                break
except FileNotFoundError:
    pass
print(int(val))
' "$file" "$METRIC" "$VENDOR" "$mode" "$result"
}

assert_metric_increase() {
  local label="$1" mode="$2" result="$3" beforefile="$4" afterfile="$5"
  local before after delta
  before="$(metric_value "$beforefile" "$mode" "$result")"
  after="$(metric_value "$afterfile" "$mode" "$result")"
  delta=$((after - before))
  if [ "$delta" -ge 1 ]; then
    record "$label" true "mode=$mode result=$result before=$before after=$after delta=$delta"
  else
    record "$label" false "mode=$mode result=$result before=$before after=$after delta=$delta(증가 없음)"
  fi
}

# start_port_forward: 지표 포트로 로컬 포트를 연결하고 응답할 때까지(최대 20초) 기다린다.
start_port_forward() {
  if [ "$DRY_RUN" = "1" ]; then
    log "DRY: kubectl --context $KUBECTL_CONTEXT -n $NAMESPACE port-forward pod/<operator pod> $LOCAL_PORT:$METRICS_PORT &"
    return 0
  fi
  local pod
  pod="$(operator_pod)"
  if [ -z "$pod" ]; then log "BLOCKED: operator pod 를 찾을 수 없음"; return 1; fi
  kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" port-forward "pod/$pod" "$LOCAL_PORT:$METRICS_PORT" >"$OUT/port-forward.log" 2>&1 &
  PF_PID=$!
  local e=0
  while [ "$e" -lt 20 ]; do
    curl -s -o /dev/null "http://127.0.0.1:$LOCAL_PORT/metrics" && return 0
    sleep 1; e=$((e + 1))
  done
  log "BLOCKED: port-forward 응답 대기 20초 초과"
  return 1
}
stop_port_forward() {
  if [ -n "$PF_PID" ]; then
    kill "$PF_PID" 2>/dev/null || true
    wait "$PF_PID" 2>/dev/null || true
    PF_PID=""
  fi
}

# metrics_snapshot LABEL — 현재 포트포워드로 /metrics 를 긁어 $OUT/metrics-LABEL.txt 에 저장.
metrics_snapshot() {
  local label="$1"
  if [ "$DRY_RUN" = "1" ]; then
    log "DRY: curl -s http://127.0.0.1:$LOCAL_PORT/metrics > $OUT/metrics-$label.txt"
    : > "$OUT/metrics-$label.txt"
    return 0
  fi
  curl -sf "http://127.0.0.1:$LOCAL_PORT/metrics" > "$OUT/metrics-$label.txt"
}

# final_scrape: 복원 후에는 지표 포트가 다시 막혀 있어야 정상이다 — 응답 없음이 PASS.
final_scrape() {
  if [ "$DRY_RUN" = "1" ]; then
    log "DRY: (원복 후) curl --max-time 3 http://127.0.0.1:$LOCAL_PORT/metrics — 실패가 정상"
    record "metrics_endpoint_closed_after_restore" true "DRY_RUN"
    return 0
  fi
  local pod fpid
  pod="$(operator_pod)"
  if [ -z "$pod" ]; then record "metrics_endpoint_closed_after_restore" false "operator pod 없음"; return 0; fi
  kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" port-forward "pod/$pod" "$LOCAL_PORT:$METRICS_PORT" >"$OUT/port-forward-final.log" 2>&1 &
  fpid=$!
  sleep 2
  if curl -s --max-time 3 -o "$OUT/metrics-final.txt" "http://127.0.0.1:$LOCAL_PORT/metrics"; then
    record "metrics_endpoint_closed_after_restore" false "원복 후에도 metrics 포트가 응답함"
  else
    record "metrics_endpoint_closed_after_restore" true "원복 후 metrics 포트 응답 없음"
  fi
  kill "$fpid" 2>/dev/null || true
  wait "$fpid" 2>/dev/null || true
}

preflight() {
  PHASE="preflight"
  log "### preflight (context=$KUBECTL_CONTEXT node=$NODE ns=$NAMESPACE release=$RELEASE acpp=$ACPP_NAME)"

  if ! kubeget version >/dev/null 2>&1; then
    log "BLOCKED: context $KUBECTL_CONTEXT 도달 불가"; BLOCKED=true; return 1
  fi
  record "context_reachable" true "$KUBECTL_CONTEXT"

  if ! kubeget get node "$NODE" >/dev/null 2>&1; then
    log "BLOCKED: 노드 $NODE 없음"; BLOCKED=true; return 1
  fi
  record "node_present" true "$NODE"

  if kubeget get acpp "$ACPP_NAME" >/dev/null 2>&1; then
    log "BLOCKED: ACPP $ACPP_NAME 이미 존재 — 이전 실행 잔존 가능성"; BLOCKED=true; return 1
  fi
  record "acpp_not_preexisting" true "$ACPP_NAME"

  local gpu_pods
  gpu_pods="$(gpu_pods_on_node_count)"
  if [ "$gpu_pods" != "0" ]; then
    log "BLOCKED: $NODE 에 $RESOURCE 요청 파드 ${gpu_pods}개"; BLOCKED=true; return 1
  fi
  record "zero_gpu_requesting_pods_on_node" true "count=$gpu_pods"

  # device-plugin 파드가 selector 로 하나라도 잡히는지 먼저 단언한다. 잘못된 DP_LABEL/DP_NS
  # 는 dp_all_running(빈 목록에서 실패)이 라이브 변경 뒤 복원 FAIL 로만 드러나므로, 여기서
  # selector 를 이름과 함께 찍어 막는다.
  local dp_pods
  dp_pods="$(kubeget -n "$DP_NS" get pods -l "$DP_LABEL" --field-selector "spec.nodeName=$NODE" -o name 2>/dev/null | grep -c . || true)"
  if [ "${dp_pods:-0}" -lt 1 ]; then
    log "BLOCKED: device-plugin 파드가 -n $DP_NS -l $DP_LABEL --field-selector spec.nodeName=$NODE 로 하나도 안 잡힘(DP_LABEL/DP_NS 확인)"
    BLOCKED=true; return 1
  fi
  record "device_plugin_pods_present" true "-n $DP_NS -l $DP_LABEL node=$NODE count=$dp_pods"

  BASE_ALLOC="$(node_alloc)"
  log "  baseline allocatable $RESOURCE=$BASE_ALLOC"
  record "baseline_allocatable_recorded" "$([ -n "$BASE_ALLOC" ] && echo true || echo false)" "$RESOURCE=$BASE_ALLOC"

  local pod; pod="$(operator_pod)"
  record "operator_pod_found" "$([ -n "$pod" ] && echo true || echo false)" "$pod"

  helmx -n "$NAMESPACE" get values "$RELEASE" -o yaml > "$VALUES_BASE" 2>/dev/null || true
  if [ -s "$VALUES_BASE" ]; then
    record "helm_values_baseline_saved" true "$VALUES_BASE"
  else
    log "BLOCKED: helm values 기준선 저장 실패(빈 파일): $VALUES_BASE"; BLOCKED=true; return 1
  fi

  DEPLOYED_TAG="$(deployed_image_tag)"
  log "  현재 image.tag=$DEPLOYED_TAG"
  record "deployed_image_tag_recorded" "$([ -n "$DEPLOYED_TAG" ] && echo true || echo false)" "$DEPLOYED_TAG"

  log "preflight OK"
}

demo() {
  PHASE="demo"

  # 1) 지표 스위치 on. 플래그를 호출 앞에 둔다(크래시-세이프).
  log "### demo 1: metrics.enabled/service.enabled=true → helm upgrade --wait"
  METRICS_ENABLED=true
  # shellcheck disable=SC2086
  run_or_print helmx upgrade "$RELEASE" "$CHART_DIR" -n "$NAMESPACE" \
    --reset-then-reuse-values --set metrics.enabled=true,metrics.service.enabled=true \
    --wait --timeout "$HELM_TIMEOUT"

  if ! start_port_forward; then
    record "port_forward_reachable" false "20초 내 미응답"
    DEMO_DONE=true; return 0
  fi
  record "port_forward_reachable" true "127.0.0.1:$LOCAL_PORT"

  # 2) baseline 스크레이프.
  log "### demo 2: baseline 스크레이프"
  metrics_snapshot baseline

  # 3) ACPP 적용(timeSliced) → Ready 대기 → ready 증가 확인.
  log "### demo 3: ACPP 적용(mode=$MODE_SHARED) → Ready 대기(최대 ${T_READY}s)"
  ACPP_APPLIED=true
  kc apply -f "$ACPP_FILE"
  if poll "$T_READY" "$IV" acpp_ready; then
    record "acpp_ready_reached" true "phase=$(acpp_phase)"
  else
    record "acpp_ready_reached" false "${T_READY}초 내 Ready 미도달(phase=$(acpp_phase))"
  fi
  metrics_snapshot after-ready
  assert_metric_increase "acpp_apply_total_ready_increased" "$MODE_SHARED" ready "$OUT/metrics-baseline.txt" "$OUT/metrics-after-ready.txt"

  # 4) exclusive patch(timeSlicing 제거) → Failed 대기 → disabled 증가 확인.
  log "### demo 4: patch mode=$MODE_EXCLUSIVE(timeSlicing 제거) → Failed 대기(최대 ${T_DISABLED}s)"
  kc patch acpp "$ACPP_NAME" --type=merge -p "{\"spec\":{\"sharing\":{\"mode\":\"$MODE_EXCLUSIVE\",\"timeSlicing\":null}}}"
  if poll "$T_DISABLED" "$IV" acpp_failed; then
    record "acpp_disable_reached" true "phase=$(acpp_phase)"
  else
    record "acpp_disable_reached" false "${T_DISABLED}초 내 Failed 미도달(phase=$(acpp_phase))"
  fi
  metrics_snapshot after-disabled
  assert_metric_increase "acpp_apply_total_disabled_increased" "$MODE_EXCLUSIVE" disabled "$OUT/metrics-after-ready.txt" "$OUT/metrics-after-disabled.txt"

  stop_port_forward
  DEMO_DONE=true
}

cleanup() {
  [ "$CLEANED" = true ] && return 0
  CLEANED=true
  PHASE="cleanup"
  log "### cleanup: (best-effort exclusive patch) → delete ACPP → device-plugin 복원 확인 → metrics 스위치 원복"

  stop_port_forward

  if [ "$ACPP_APPLIED" = true ]; then
    local exclusive_patch="{\"spec\":{\"sharing\":{\"mode\":\"$MODE_EXCLUSIVE\",\"timeSlicing\":null}}}"
    # best-effort exclusive patch: 실제 실행에서는 이미 없는 객체 등의 오류를 삼키고,
    # DRY_RUN 에서는 kc() 가 찍는 명령 출력(+ kubectl …)을 그대로 보이게 둔다.
    if [ "$DRY_RUN" = "1" ]; then
      kc patch acpp "$ACPP_NAME" --type=merge -p "$exclusive_patch" || true
    else
      kc patch acpp "$ACPP_NAME" --type=merge -p "$exclusive_patch" >/dev/null 2>&1 || true
    fi
    kc delete acpp "$ACPP_NAME" --ignore-not-found >&2 || true
    if poll "$T_RESTORE" "$IV" restored_after_delete; then
      record "device_plugin_and_allocatable_restored" true "$RESOURCE=$(node_alloc)"
    else
      record "device_plugin_and_allocatable_restored" false "${T_RESTORE}초 내 미복원($RESOURCE=$(node_alloc))"
    fi
  fi

  if [ "$METRICS_ENABLED" = true ]; then
    log "  metrics 스위치 원복: --reset-values -f $VALUES_BASE --set image.tag=$DEPLOYED_TAG"
    # shellcheck disable=SC2086
    run_or_print helmx upgrade "$RELEASE" "$CHART_DIR" -n "$NAMESPACE" \
      --reset-values -f "$VALUES_BASE" --set "image.tag=$DEPLOYED_TAG" \
      --wait --timeout "$HELM_TIMEOUT" || log "  경고: metrics 원복 helm upgrade 실패 — 수동 확인 필요"
    final_scrape
  fi
}

finish() {
  if [ "$CLEANED" != true ]; then cleanup || true; fi
  local verdict="FAIL" dry="false"
  if [ "$DRY_RUN" = "1" ]; then verdict="DRY"; dry="true"
  elif [ "$BLOCKED" = true ]; then verdict="BLOCKED"
  elif [ "$DEMO_DONE" = true ] && [ "$FAILS" -eq 0 ]; then verdict="PASS"
  fi
  printf '{"test":"nvidia-acpp-metrics","vendor":"%s","node":"%s","acpp":"%s","metric":"%s","phase":"%s","checks":{%s},"fails":%s,"verdict":"%s","dryRun":%s,"date":"%s"}\n' \
    "$VENDOR" "$NODE" "$ACPP_NAME" "$METRIC" "$PHASE" "$CHECK_JSON" "$FAILS" "$verdict" "$dry" "$(utc_now)" \
    | tee -a "$OUT/summary-$(date +%H%M%S).json"
  [ "$verdict" = "PASS" ] || [ "$verdict" = "DRY" ] || exit 1
  exit 0
}

trap finish EXIT
# 신호로 끊겨도 EXIT 트랩(정리)이 돌도록 신호를 종료 코드로 바꾼다(128+신호 번호) —
# 트랩이 없는 SIGTERM·SIGHUP 은 셸을 바로 끝내 EXIT 트랩이 돌지 않는다.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
preflight || true
if [ "$BLOCKED" != true ]; then demo; fi
# EXIT 트랩(finish)이 cleanup + JSON 요약 + 종료 코드를 담당한다.
