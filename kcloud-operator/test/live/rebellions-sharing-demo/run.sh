#!/usr/bin/env bash
# ============================================================
# run.sh: Rebellions ATOM descriptor SW 시분할 라이브 데모(<rebellions-node>) — 유휴 재실측 판정의 공유 경로 확인
# 상세: NPUClusterPolicy 를 끄고 descriptor 적용→소비자 2개 적용→판정→복원 순서로 돈다(trap 으로
#       실패·중단 시에도 복원). 자체 descriptor·소비자 파드·NCP 의 rebellions.enabled 필드만 변경한다.
# 생성일: 2026-09-22 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

DRY_RUN="${DRY_RUN:-0}"
KUBECTL_CONTEXT="${KUBECTL_CONTEXT:-kubernetes-admin@cluster.local}"

NAMESPACE="${NAMESPACE:-kcloud}"        # NPUClusterPolicy 네임스페이스
NCP="${NCP:-npuclusterpolicy-sample}"
VENDOR="${VENDOR:-rebellions}"
NODE="${NODE:-node5}"

NCP_DS_NS="${NCP_DS_NS:-kube-system}"   # NPUClusterPolicy 가 관리하는 벤더 device-plugin
NCP_DS="${NCP_DS:-rbln-device-plugin}"
GEN_NS="${GEN_NS:-kcloud}"              # 생성물(device-plugin) 네임스페이스 = operator ns

DESC_FILE="${DESC_FILE:-$SCRIPT_DIR/descriptor.yaml}"
CONSUMER1_FILE="${CONSUMER1_FILE:-$SCRIPT_DIR/consumer-1.yaml}"
CONSUMER2_FILE="${CONSUMER2_FILE:-$SCRIPT_DIR/consumer-2.yaml}"

name_from() { sed -nE 's/^[[:space:]]*name:[[:space:]]*([^[:space:]]+).*/\1/p' "$1" | head -1; }
DESC_NAME="${DESC_NAME:-$(name_from "$DESC_FILE")}"
C1_NAME="${C1_NAME:-$(name_from "$CONSUMER1_FILE")}"
C2_NAME="${C2_NAME:-$(name_from "$CONSUMER2_FILE")}"
CONSUMER_NS="${CONSUMER_NS:-default}"
GEN_DS="kcloud-generated-${DESC_NAME}-dp"   # generated_backend.go 이름 규칙

RESOURCE_DESC="${RESOURCE_DESC:-rebellions/atom}"    # descriptor 가 광고(vendor/product)
RESOURCE_VENDOR="${RESOURCE_VENDOR:-rebellions.ai/ATOM}"  # NPUClusterPolicy 벤더 광고
REPLICAS="${REPLICAS:-2}"

# 대기 상한(초)·간격.
T_DS_DELETE="${T_DS_DELETE:-240}"
T_GEN_UP="${T_GEN_UP:-180}"
T_CONSUMER="${T_CONSUMER:-180}"
T_GC="${T_GC:-180}"
T_RESTORE="${T_RESTORE:-300}"
IV="${IV:-10}"

OUT="${OUT:-/tmp/rebellions-sharing-demo-evidence}"
mkdir -p "$OUT"

RESOURCE_DESC_JP="${RESOURCE_DESC//./\\.}"
RESOURCE_VENDOR_JP="${RESOURCE_VENDOR//./\\.}"

ORIG_ENABLED=""
NCP_PATCHED=false
DEMO_DONE=false
BLOCKED=false
DESC_APPLIED=false
CONSUMERS_APPLIED=false
CLEANED=false
CHECK_JSON=""
FAILS=0
PHASE="init"

utc_now() { date -u +%FT%TZ; }
log() { printf '%s\n' "$*" >&2; }

kc() {
  if [ "$DRY_RUN" = "1" ]; then printf '+ kubectl --context %s %s\n' "$KUBECTL_CONTEXT" "$*" >&2; return 0; fi
  kubectl --context "$KUBECTL_CONTEXT" "$@"
}
kubeget() { if [ "$DRY_RUN" = "1" ]; then return 0; fi; kubectl --context "$KUBECTL_CONTEXT" "$@"; }

record() {
  local name="$1" ok="$2" detail="${3:-}"
  if [ "$ok" != "true" ]; then FAILS=$((FAILS + 1)); fi
  CHECK_JSON="${CHECK_JSON:+$CHECK_JSON,}\"$name\":$ok"
  local tag; tag=$([ "$ok" = "true" ] && echo PASS || echo FAIL)
  log "  [$tag] $name ${detail:+— $detail}"
}

ncp_patch() { kc -n "$NAMESPACE" patch npuclusterpolicy "$NCP" --type=merge -p "$1" >&2; }
ncp_ds_present() { kubeget -n "$NCP_DS_NS" get ds "$NCP_DS" >/dev/null 2>&1; }
ncp_ds_absent()  { ! ncp_ds_present; }
gen_ds_present() { kubeget -n "$GEN_NS" get ds "$GEN_DS" >/dev/null 2>&1; }
gen_ds_absent()  { ! gen_ds_present; }
ds_ready() { kubeget -n "$1" get ds "$2" -o jsonpath='{.status.numberReady}' 2>/dev/null || echo ""; }
alloc_desc()   { kubeget get node "$NODE" -o jsonpath="{.status.allocatable.$RESOURCE_DESC_JP}" 2>/dev/null || echo ""; }
alloc_vendor() { kubeget get node "$NODE" -o jsonpath="{.status.allocatable.$RESOURCE_VENDOR_JP}" 2>/dev/null || echo ""; }
# ncp_ds_up: 벤더 device-plugin 이 뜨고(ready≥1) 벤더 리소스가 다시 광고되는지 — 복원은 DS
# 오브젝트 재생성이 아니라 실제 광고 재개까지 봐야 한다(DS present 만으론 ready=0 순간에 통과).
ncp_ds_up() { local r; r="$(ds_ready "$NCP_DS_NS" "$NCP_DS")"; [ "${r:-0}" -ge 1 ] 2>/dev/null && [ -n "$(alloc_vendor)" ] && [ "$(alloc_vendor)" != "0" ]; }
pod_phase() { kubeget -n "$CONSUMER_NS" get pod "$1" -o jsonpath='{.status.phase}' 2>/dev/null || echo ""; }
pod_running() { local p; p="$(pod_phase "$1")"; [ "$p" = "Running" ] || [ "$p" = "Succeeded" ]; }
both_running() { pod_running "$C1_NAME" && pod_running "$C2_NAME"; }

# desc 가 replicas 만큼 광고했는가.
gen_advertised_n() {
  local a; a="$(alloc_desc)"
  [ -n "$a" ] && [ "$a" != "0" ] && [ "${a:-0}" -ge "$REPLICAS" ] 2>/dev/null
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

dump_desc_status() {
  [ "$DRY_RUN" = "1" ] && return 0
  log "  --- descriptor status(refusals/feasible) ---"
  kubectl --context "$KUBECTL_CONTEXT" get acceleratordescriptor "$DESC_NAME" -o jsonpath='{"feasible="}{.status.feasibleBackends}{" refusals="}{range .status.refusals[*]}{.backend}{": "}{.reason}{" | "}{end}{"\n"}' 2>&1 | tee "$OUT/descriptor-status.txt" >&2 || true
}

preflight() {
  PHASE="preflight"
  log "### preflight (vendor=$VENDOR node=$NODE ncp_ds=$NCP_DS_NS/$NCP_DS gen_ds=$GEN_NS/$GEN_DS desc=$DESC_NAME resource=$RESOURCE_DESC replicas=$REPLICAS)"
  if [ "$DRY_RUN" = "1" ]; then log "  [DRY_RUN] preflight 생략"; return 0; fi

  if ! kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get npuclusterpolicy "$NCP" >/dev/null 2>&1; then
    log "BLOCKED: NPUClusterPolicy $NAMESPACE/$NCP 없음 — 데모 중단"; BLOCKED=true; return 1
  fi
  ORIG_ENABLED="$(kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get npuclusterpolicy "$NCP" -o jsonpath="{.spec.$VENDOR.enabled}" 2>/dev/null || echo "")"
  log "  원값: $VENDOR.enabled=$ORIG_ENABLED"
  log "  시작: ncp_ds=$(ncp_ds_present && echo yes || echo no) ready=$(ds_ready "$NCP_DS_NS" "$NCP_DS") vendor_alloc=$(alloc_vendor) desc_alloc=$(alloc_desc)"
  record "start_vendor_advertised" "$([ -n "$(alloc_vendor)" ] && [ "$(alloc_vendor)" != "0" ] && echo true || echo false)" "$RESOURCE_VENDOR=$(alloc_vendor)"
  if [ "$ORIG_ENABLED" != "true" ]; then
    log "BLOCKED: $VENDOR.enabled 이 true 가 아님(='$ORIG_ENABLED') — 중단"; BLOCKED=true; return 1
  fi
}

demo() {
  PHASE="demo"

  # 1) rebellions.enabled=false → 소유 device-plugin 회수.
  log "### demo 1: $VENDOR.enabled=false → $NCP_DS_NS/$NCP_DS 삭제"
  # 플래그를 patch 앞에 둔다 — patch 와 대입 사이에서 중단돼도 cleanup 이 복원한다.
  NCP_PATCHED=true
  ncp_patch "{\"spec\":{\"$VENDOR\":{\"enabled\":false}}}"
  if poll "$T_DS_DELETE" "$IV" ncp_ds_absent; then
    record "ncp_ds_deleted_on_disable" true "$NCP_DS_NS/$NCP_DS 삭제됨"
  else
    record "ncp_ds_deleted_on_disable" false "$T_DS_DELETE 초 내 미삭제"
  fi

  # 2) descriptor 적용 → 생성 device-plugin 이 rebellions/atom 을 replicas 개로 광고.
  log "### demo 2: descriptor 적용 → 생성 device-plugin + 광고(count $REPLICAS)"
  # 플래그를 apply 앞에 둔다 — apply 와 대입 사이에서 중단돼도 cleanup 이 회수한다.
  DESC_APPLIED=true
  kc apply -f "$DESC_FILE" >&2
  if poll "$T_GEN_UP" "$IV" gen_advertised_n; then
    record "descriptor_advertised_n" true "$RESOURCE_DESC=$(alloc_desc) (gen_ds ready=$(ds_ready "$GEN_NS" "$GEN_DS"))"
  else
    record "descriptor_advertised_n" false "$T_GEN_UP 초 내 $RESOURCE_DESC 가 $REPLICAS 로 광고되지 않음(현재=$(alloc_desc) gen_ds ready=$(ds_ready "$GEN_NS" "$GEN_DS"))"
    dump_desc_status
    DEMO_DONE=true   # 데모 절차는 끝까지 진행했다(광고 실패는 record 로 남음). cleanup 이 복원.
    return 0
  fi

  # 3) 소비자 2개 → 둘 다 Running + 같은 /dev/rbln0 open(OPEN_OK) 동시.
  log "### demo 3: 소비자 2개 적용 → 둘 다 Running + OPEN_OK"
  kc apply -f "$CONSUMER1_FILE" >&2
  kc apply -f "$CONSUMER2_FILE" >&2
  CONSUMERS_APPLIED=true
  if poll "$T_CONSUMER" "$IV" both_running; then
    record "both_consumers_running" true "c1=$(pod_phase "$C1_NAME") c2=$(pod_phase "$C2_NAME")"
  else
    record "both_consumers_running" false "$T_CONSUMER 초 내 둘 다 Running 아님(c1=$(pod_phase "$C1_NAME") c2=$(pod_phase "$C2_NAME"))"
  fi
  # 둘 다 Running 인 시점의 스냅샷(겹침 증거).
  kubeget -n "$CONSUMER_NS" get pods "$C1_NAME" "$C2_NAME" -o wide 2>&1 | tee "$OUT/consumers-wide.txt" >&2 || true

  local l1 l2
  l1="$(kubectl --context "$KUBECTL_CONTEXT" -n "$CONSUMER_NS" logs "$C1_NAME" 2>/dev/null || echo "")"
  l2="$(kubectl --context "$KUBECTL_CONTEXT" -n "$CONSUMER_NS" logs "$C2_NAME" 2>/dev/null || echo "")"
  printf '%s\n' "$l1" > "$OUT/consumer-1.log"
  printf '%s\n' "$l2" > "$OUT/consumer-2.log"
  if printf '%s' "$l1" | grep -q 'OPEN_OK' && printf '%s' "$l2" | grep -q 'OPEN_OK'; then
    record "both_open_ok" true "c1='$(printf '%s' "$l1" | grep OPEN_OK | head -1)' c2='$(printf '%s' "$l2" | grep OPEN_OK | head -1)'"
  else
    record "both_open_ok" false "c1='$(printf '%s' "$l1" | tr '\n' ' ' | cut -c1-120)' c2='$(printf '%s' "$l2" | tr '\n' ' ' | cut -c1-120)'"
  fi
  DEMO_DONE=true
}

cleanup() {
  [ "$CLEANED" = true ] && return 0
  CLEANED=true
  PHASE="cleanup"
  log "### cleanup: 소비자·descriptor 제거 → GC → $VENDOR 재활성 복원"

  if [ "$CONSUMERS_APPLIED" = true ]; then
    kc -n "$CONSUMER_NS" delete pod "$C1_NAME" "$C2_NAME" --ignore-not-found >&2 || true
    CONSUMERS_APPLIED=false
  fi
  if [ "$DESC_APPLIED" = true ]; then
    kc delete acceleratordescriptor "$DESC_NAME" --ignore-not-found >&2 || true
    DESC_APPLIED=false
    if poll "$T_GC" "$IV" gen_ds_absent; then
      record "generated_dp_gc" true "$GEN_NS/$GEN_DS 회수됨"
    else
      record "generated_dp_gc" false "$T_GC 초 내 $GEN_NS/$GEN_DS 미회수"
    fi
  fi
  if [ "$NCP_PATCHED" = true ]; then
    ncp_patch "{\"spec\":{\"$VENDOR\":{\"enabled\":${ORIG_ENABLED:-true}}}}" 2>/dev/null || \
      log "  경고: 복원 패치 실패 — 수동 확인 필요"
    if poll "$T_RESTORE" "$IV" ncp_ds_up; then
      record "restored_vendor_dp" true "ready=$(ds_ready "$NCP_DS_NS" "$NCP_DS") $RESOURCE_VENDOR=$(alloc_vendor)"
    else
      record "restored_vendor_dp" false "$T_RESTORE 초 내 미복원(ready=$(ds_ready "$NCP_DS_NS" "$NCP_DS") $RESOURCE_VENDOR=$(alloc_vendor))"
    fi
  else
    log "  복원 불필요 — NPUClusterPolicy 를 패치한 적 없음"
  fi
}

finish() {
  if [ "$CLEANED" != true ]; then cleanup || true; fi
  local verdict="FAIL" dry="false"
  if [ "$DRY_RUN" = "1" ]; then verdict="DRY"; dry="true"
  elif [ "$BLOCKED" = true ]; then verdict="BLOCKED"
  elif [ "$DEMO_DONE" = true ] && [ "$FAILS" -eq 0 ]; then verdict="PASS"
  fi
  printf '{"test":"rebellions-sharing-demo","vendor":"%s","node":"%s","descriptor":"%s","resource":"%s","replicas":%s,"phase":"%s","checks":{%s},"fails":%s,"verdict":"%s","dryRun":%s,"date":"%s"}\n' \
    "$VENDOR" "$NODE" "$DESC_NAME" "$RESOURCE_DESC" "$REPLICAS" "$PHASE" "$CHECK_JSON" "$FAILS" "$verdict" "$dry" "$(utc_now)" \
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
# EXIT trap(finish) 이 cleanup + JSON 요약 + 종료코드를 담당한다.
