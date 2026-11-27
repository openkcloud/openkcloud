#!/usr/bin/env bash
# ============================================================
# run.sh: managementMode=external 라이브 데모(preflight/demo/cleanup)
# 상세: 한 벤더를 external 로 돌려 device-plugin DaemonSet 부재·라벨 유지·Ready 사유
#       (ExternalManaged)·descriptor 거절을 확인한다. 자체 NCP 필드·자체 descriptor 만
#       건드리고 외부 워크로드는 손대지 않는다(복원은 cleanup() 참고). 전환 중 그 벤더
#       device-plugin 이 잠시 내려가므로 데모/실험 클러스터에서 실행한다.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

VENDOR="${VENDOR:-tenstorrent}"
NAMESPACE="${NAMESPACE:-kcloud}"
NCP="${NCP:-npuclusterpolicy-sample}"

# 프로파일: 벤더별 DaemonSet/노드/리소스/라벨/descriptor 매니페스트.
case "$VENDOR" in
  tenstorrent)
    DS_NS="${DS_NS:-kcloud}"
    DS_NAME="${DS_NAME:-kcloud-tt-device-plugin}"
    NODE="${NODE:-k8s-worker3}"
    RESOURCE="${RESOURCE:-tenstorrent.com/blackhole}"
    LABEL_PREFIX="${LABEL_PREFIX:-kcloud.ai/tenstorrent}"
    DESC_FILE="${DESC_FILE:-$SCRIPT_DIR/descriptor-tenstorrent.yaml}"
    DESC_NAME="${DESC_NAME:-ext-mode-tt-refusal-probe}"
    ;;
  nvidia)
    DS_NS="${DS_NS:-kube-system}"
    DS_NAME="${DS_NAME:-nvidia-device-plugin-flat}"
    NODE="${NODE:-k8s-worker1}"
    RESOURCE="${RESOURCE:-nvidia.com/gpu}"
    LABEL_PREFIX="${LABEL_PREFIX:-kcloud.ai/nvidia}"
    DESC_FILE="${DESC_FILE:-$SCRIPT_DIR/descriptor-nvidia.yaml}"
    DESC_NAME="${DESC_NAME:-ext-mode-nvidia-refusal-probe}"
    ;;
  *)
    echo "unknown VENDOR=$VENDOR (tenstorrent|nvidia)" >&2
    exit 2
    ;;
esac

# 대기 상한(초)·간격. 필요하면 env 로 덮는다.
T_DELETE="${T_DELETE:-240}"
T_EXTERNAL="${T_EXTERNAL:-120}"
T_DESC="${T_DESC:-90}"
T_RESTORE="${T_RESTORE:-300}"
IV="${IV:-10}"
IV_DESC="${IV_DESC:-5}"

OUT="${OUT:-/tmp/external-mode-evidence}"
mkdir -p "$OUT"

# jsonpath 는 키의 모든 '.' 를 이스케이프해야 한다(예: nvidia\.com/gpu,
# kcloud\.ai/tenstorrent\.present). '/' 는 그대로 둔다.
RESOURCE_JP="${RESOURCE//./\\.}"
LABEL_KEY="${LABEL_PREFIX}.present"
LABEL_JP="${LABEL_KEY//./\\.}"

# 상태 변수(원값·진행 플래그·검사 집계).
ORIG_ENABLED="true"
ORIG_MODE=""
DEMO_DONE=false
BLOCKED=false
DESC_APPLIED=false
CLEANED=false
CHECK_JSON=""
FAILS=0
PHASE="init"

utc_now() { date -u +%FT%TZ; }

log() { printf '%s\n' "$*" >&2; }

# record NAME true|false [detail] — 검사 결과를 집계한다(절대 중단하지 않는다).
record() {
  local name="$1" ok="$2" detail="${3:-}"
  if [ "$ok" != "true" ]; then FAILS=$((FAILS + 1)); fi
  CHECK_JSON="${CHECK_JSON:+$CHECK_JSON,}\"$name\":$ok"
  local tag; tag=$([ "$ok" = "true" ] && echo PASS || echo FAIL)
  log "  [$tag] $name ${detail:+— $detail}"
}

ncp_patch() { kubectl -n "$NAMESPACE" patch npuclusterpolicy "$NCP" --type=merge -p "$1" >&2; }

ds_present() { kubectl -n "$DS_NS" get ds "$DS_NAME" >/dev/null 2>&1; }
ds_absent()  { ! ds_present; }
ds_ready()   { kubectl -n "$DS_NS" get ds "$DS_NAME" -o jsonpath='{.status.numberReady}' 2>/dev/null || echo ""; }
present_label() { kubectl get node "$NODE" -o jsonpath="{.metadata.labels.$LABEL_JP}" 2>/dev/null || echo ""; }
allocatable() { kubectl get node "$NODE" -o jsonpath="{.status.allocatable.$RESOURCE_JP}" 2>/dev/null || echo ""; }
ready_reason() {
  kubectl -n "$NAMESPACE" get npuclusterpolicy "$NCP" -o json 2>/dev/null | python3 -c '
import json,sys
try: s=json.load(sys.stdin).get("status",{})
except Exception: print(""); sys.exit(0)
for c in s.get("conditions",[]):
    if c.get("type")=="Ready": print(c.get("reason","")); break
' 2>/dev/null || echo ""
}
ds_up() { # DS numberReady>=1 이고 리소스 allocatable 이 비어있지 않으면 0.
  local r a; r="$(ds_ready)"; a="$(allocatable)"
  [ "${r:-0}" -ge 1 ] 2>/dev/null && [ -n "$a" ]
}

reason_is_external() { [ "$(ready_reason)" = "ExternalManaged" ]; }
refusals_present() { [ -n "$(kubectl get acceleratordescriptor "$1" -o jsonpath='{.status.refusals}' 2>/dev/null)" ]; }

# poll TIMEOUT INTERVAL predicate... — predicate 가 성공할 때까지 최대 TIMEOUT 초 대기.
poll() {
  local t="$1" iv="$2"; shift 2
  local e=0
  while [ "$e" -lt "$t" ]; do
    if "$@"; then return 0; fi
    sleep "$iv"; e=$((e + iv))
  done
  return 1
}

# ── preflight: 원값 저장 + 시작 상태 기록 ─────────────────────────────────
preflight() {
  PHASE="preflight"
  log "### preflight (vendor=$VENDOR node=$NODE ds=$DS_NS/$DS_NAME resource=$RESOURCE)"

  if ! kubectl -n "$NAMESPACE" get npuclusterpolicy "$NCP" >/dev/null 2>&1; then
    log "BLOCKED: NPUClusterPolicy $NAMESPACE/$NCP 없음 — 데모 중단"
    BLOCKED=true
    return 1
  fi

  local e m
  e="$(kubectl -n "$NAMESPACE" get npuclusterpolicy "$NCP" -o jsonpath="{.spec.$VENDOR.enabled}" 2>/dev/null || echo "")"
  m="$(kubectl -n "$NAMESPACE" get npuclusterpolicy "$NCP" -o jsonpath="{.spec.$VENDOR.managementMode}" 2>/dev/null || echo "")"
  [ -n "$e" ] && ORIG_ENABLED="$e"
  ORIG_MODE="$m"
  log "  원값: enabled=$ORIG_ENABLED managementMode='${ORIG_MODE:-<empty>}'"
  log "  시작: ds_present=$(ds_present && echo yes || echo no) ds_ready=$(ds_ready) present_label=$(present_label) allocatable=$(allocatable) ready_reason=$(ready_reason)"

  if [ "$ORIG_MODE" = "external" ]; then
    log "BLOCKED: $VENDOR 가 이미 external — 데모의 전환/복원 대상이 아님, 중단"
    BLOCKED=true
    return 1
  fi
}

# ── demo: 비활성화로 owned DS 삭제 → external 전환 → descriptor 거절 ───────
demo() {
  PHASE="demo"

  # 1) managed + enabled=false → 소유 DaemonSet 회수(토글 off 삭제 경로).
  log "### demo 1: $VENDOR.enabled=false → owned DaemonSet 삭제"
  ncp_patch "{\"spec\":{\"$VENDOR\":{\"enabled\":false}}}"
  if poll "$T_DELETE" "$IV" ds_absent; then
    record "ds_deleted_on_disable" true "$DS_NS/$DS_NAME 삭제됨"
  else
    record "ds_deleted_on_disable" false "$T_DELETE 초 내 $DS_NS/$DS_NAME 미삭제"
  fi

  # 2) enabled=true + managementMode=external → DS 부재 유지·라벨 유지·Ready=ExternalManaged.
  log "### demo 2: $VENDOR.enabled=true + managementMode=external"
  ncp_patch "{\"spec\":{\"$VENDOR\":{\"enabled\":true,\"managementMode\":\"external\"}}}"
  poll "$T_EXTERNAL" "$IV" reason_is_external || true

  local reason label
  reason="$(ready_reason)"
  label="$(present_label)"
  if ds_absent; then
    record "ds_absent_while_external" true "$DS_NS/$DS_NAME 없음"
  else
    record "ds_absent_while_external" false "$DS_NS/$DS_NAME 존재(ready=$(ds_ready))"
  fi
  [ "$label" = "true" ] && record "present_label_kept" true "$LABEL_PREFIX.present=true" \
    || record "present_label_kept" false "$LABEL_PREFIX.present='$label'"
  [ "$reason" = "ExternalManaged" ] && record "ready_reason_externalmanaged" true "reason=$reason" \
    || record "ready_reason_externalmanaged" false "reason='$reason'"

  # 3) external 벤더 descriptor → 중재 거절.
  log "### demo 3: descriptor 적용 → 중재 거절 확인 ($DESC_FILE)"
  kubectl apply -f "$DESC_FILE" >&2
  DESC_APPLIED=true
  local dname refusals
  dname="$DESC_NAME"
  poll "$T_DESC" "$IV_DESC" refusals_present "$dname" || true
  refusals="$(kubectl get acceleratordescriptor "$dname" -o jsonpath='{.status.refusals}' 2>/dev/null || echo "")"
  if printf '%s' "$refusals" | grep -q "NPUClusterPolicy 가 관리 중"; then
    record "descriptor_refused" true "refusal 사유에 'NPUClusterPolicy 가 관리 중' 포함"
  else
    record "descriptor_refused" false "refusals='$(printf '%s' "$refusals" | cut -c1-160)'"
  fi
  kubectl delete acceleratordescriptor "$dname" --ignore-not-found >&2 || true
  DESC_APPLIED=false

  # demo 가 끝까지 실행됐음을 표시한다. set -e 로 중간 명령이 실패하면 이 줄에 닿지 못하므로,
  # finish 의 PASS 조건(DEMO_DONE=true)이 성립하지 않아 중단된 데모가 PASS 로 새지 않는다.
  DEMO_DONE=true
}

# ── cleanup: managementMode 제거 + enabled 원복 → DS·광고 복귀(항상 실행) ──
cleanup() {
  [ "$CLEANED" = true ] && return 0
  CLEANED=true
  PHASE="cleanup"
  log "### cleanup: managementMode 제거 + enabled=$ORIG_ENABLED 복원"

  # 데모 중 남았을 수 있는 descriptor 를 먼저 지운다.
  if [ "$DESC_APPLIED" = true ]; then
    kubectl delete acceleratordescriptor "$DESC_NAME" --ignore-not-found >&2 2>/dev/null || true
    DESC_APPLIED=false
  fi

  # managementMode 는 null 로 제거(enum 에 "" 없음 → 키 제거 = managed). 원래 external 이
  # 아니었던 전제이므로 항상 null 로 되돌린다. enabled 는 원값으로.
  ncp_patch "{\"spec\":{\"$VENDOR\":{\"managementMode\":null,\"enabled\":$ORIG_ENABLED}}}" 2>/dev/null || \
    log "  경고: 복원 패치 실패 — 수동 확인 필요"

  if [ "$ORIG_ENABLED" = "true" ]; then
    if poll "$T_RESTORE" "$IV" ds_up; then
      record "restored_ds_ready" true "ready=$(ds_ready)"
      record "restored_allocatable" true "$RESOURCE=$(allocatable)"
    else
      record "restored_ds_ready" false "$T_RESTORE 초 내 ready>=1 미복귀(ready=$(ds_ready))"
      record "restored_allocatable" false "$RESOURCE=$(allocatable)"
    fi
  else
    log "  원래 enabled=false 였음 — DS 복귀 검사 생략"
  fi
}

# ── finish: cleanup 보장 + JSON 요약 한 줄 + 종료코드 ──────────────────────
finish() {
  if [ "$CLEANED" != true ]; then cleanup || true; fi
  local verdict="FAIL"
  if [ "$BLOCKED" = true ]; then
    verdict="BLOCKED"
  elif [ "$DEMO_DONE" = true ] && [ "$FAILS" -eq 0 ]; then
    verdict="PASS"
  fi
  printf '{"test":"external-mode","vendor":"%s","node":"%s","ns":"%s","ncp":"%s","phase":"%s","checks":{%s},"fails":%s,"verdict":"%s","date":"%s"}\n' \
    "$VENDOR" "$NODE" "$NAMESPACE" "$NCP" "$PHASE" "$CHECK_JSON" "$FAILS" "$verdict" "$(utc_now)" | tee -a "$OUT/summary-$(date +%H%M%S).json"
  [ "$verdict" = "PASS" ] || exit 1
  exit 0
}

trap finish EXIT
# 신호로 끊겨도 EXIT 트랩(정리)이 돌도록 신호를 종료 코드로 바꾼다(128+신호 번호) —
# 트랩이 없는 SIGTERM·SIGHUP 은 셸을 바로 끝내 EXIT 트랩이 돌지 않는다.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

preflight || true
if [ "$BLOCKED" != true ]; then
  demo
fi
# EXIT trap(finish) 이 cleanup 실행 + JSON 요약 출력 + 종료코드를 담당한다.
