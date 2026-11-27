#!/usr/bin/env bash
# ============================================================
# run.sh: "새 벤더 = device-plugin 코드 0줄" 라이브 데모 — descriptor 로 Tenstorrent 광고·소비·복원
# 상세: tenstorrent 를 끄면 소유 device-plugin 이 회수되고, descriptor 하나를 적용하면 벤더
#       코드 없이 생성된 device-plugin 이 떠 장치를 광고한다. 소비자 파드로 주입을 확인한 뒤
#       복원한다(cleanup() 참고). 자체 descriptor·파드·NCP.tenstorrent 필드만 건드리며, 전환
#       중 그 벤더 device-plugin 이 잠시 내려가므로 데모/실험 클러스터(<tenstorrent-node>)에서 실행.
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 시나리오 성격(기록용).
SCENARIO_MINUTES=6
SCENARIO_DESTRUCTIVE="no"

DRY_RUN="${DRY_RUN:-0}"

NAMESPACE="${NAMESPACE:-kcloud}"       # NPUClusterPolicy + 생성물 네임스페이스
NCP="${NCP:-npuclusterpolicy-sample}"
VENDOR="${VENDOR:-tenstorrent}"
NODE="${NODE:-k8s-worker3}"

NCP_DS_NS="${NCP_DS_NS:-kcloud}"
NCP_DS="${NCP_DS:-kcloud-tt-device-plugin}"   # NPUClusterPolicy 가 관리하는 벤더 device-plugin
GEN_NS="${GEN_NS:-kcloud}"                     # 생성물(device-plugin) 네임스페이스 = operator ns

DESC_FILE="${DESC_FILE:-$SCRIPT_DIR/descriptor.yaml}"
CONSUMER_FILE="${CONSUMER_FILE:-$SCRIPT_DIR/consumer.yaml}"

# 이름은 매니페스트에서 읽어 스크립트와 파일이 어긋나지 않게 한다.
name_from() { sed -nE 's/^[[:space:]]*name:[[:space:]]*([^[:space:]]+).*/\1/p' "$1" | head -1; }
DESC_NAME="${DESC_NAME:-$(name_from "$DESC_FILE")}"
CONSUMER_NAME="${CONSUMER_NAME:-$(name_from "$CONSUMER_FILE")}"
CONSUMER_NS="${CONSUMER_NS:-default}"
GEN_DS="kcloud-generated-${DESC_NAME}-dp"      # generated_backend.go 의 이름 규칙

RESOURCE_DESC="${RESOURCE_DESC:-tenstorrent/blackhole-p150}"   # descriptor 가 광고(vendor/product)
RESOURCE_VENDOR="${RESOURCE_VENDOR:-tenstorrent.com/blackhole}" # NPUClusterPolicy 벤더 광고

# 대기 상한(초)·간격.
T_DS_DELETE="${T_DS_DELETE:-240}"
T_GEN_UP="${T_GEN_UP:-180}"
T_CONSUMER="${T_CONSUMER:-120}"
T_GC="${T_GC:-180}"
T_RESTORE="${T_RESTORE:-300}"
IV="${IV:-10}"

OUT="${OUT:-/tmp/new-vendor-by-descriptor-evidence}"
mkdir -p "$OUT"

# jsonpath 는 키의 '.' 를 이스케이프한다. '/' 는 그대로.
RESOURCE_DESC_JP="${RESOURCE_DESC//./\\.}"
RESOURCE_VENDOR_JP="${RESOURCE_VENDOR//./\\.}"

# 상태 변수.
ORIG_ENABLED=""
NCP_PATCHED=false   # NPUClusterPolicy 를 실제로 disable 패치했는가 — 복원은 이때만 한다
DEMO_DONE=false
BLOCKED=false
DESC_APPLIED=false
CONSUMER_APPLIED=false
CLEANED=false
CHECK_JSON=""
FAILS=0
PHASE="init"

utc_now() { date -u +%FT%TZ; }
log() { printf '%s\n' "$*" >&2; }

# kc: 상태를 바꾸는 kubectl 래퍼. DRY_RUN 이면 명령만 찍고 실행하지 않는다.
kc() {
  if [ "$DRY_RUN" = "1" ]; then
    printf '+ kubectl %s\n' "$*" >&2
    return 0
  fi
  kubectl "$@"
}

# kubeget: 읽기 전용 kubectl 래퍼. DRY_RUN 이면 클러스터에 접근하지 않고 빈 값을 낸다
# (detail 문자열·predicate 가 오프라인에서도 흐르게 한다).
kubeget() {
  if [ "$DRY_RUN" = "1" ]; then return 0; fi
  kubectl "$@"
}

# record NAME true|false [detail] — 검사 결과 집계(중단하지 않음).
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

gen_ds_up() { local r; r="$(ds_ready "$GEN_NS" "$GEN_DS")"; [ "${r:-0}" -ge 1 ] 2>/dev/null && [ -n "$(alloc_desc)" ]; }
ncp_ds_up() { local r; r="$(ds_ready "$NCP_DS_NS" "$NCP_DS")"; [ "${r:-0}" -ge 1 ] 2>/dev/null && [ -n "$(alloc_vendor)" ]; }
gen_advertised() { local a; a="$(alloc_desc)"; [ -n "$a" ] && [ "$a" != "0" ]; }
pod_phase() { kubeget -n "$CONSUMER_NS" get pod "$CONSUMER_NAME" -o jsonpath='{.status.phase}' 2>/dev/null || echo ""; }
consumer_ready() { local p; p="$(pod_phase)"; [ "$p" = "Running" ] || [ "$p" = "Succeeded" ]; }

# poll TIMEOUT INTERVAL predicate... — predicate 성공까지 최대 TIMEOUT 초 대기.
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

# ── preflight: 원값 저장 + 시작 상태 기록 ─────────────────────────────────
preflight() {
  PHASE="preflight"
  log "### preflight (vendor=$VENDOR node=$NODE ncp_ds=$NCP_DS_NS/$NCP_DS gen_ds=$GEN_NS/$GEN_DS desc=$DESC_NAME)"

  if [ "$DRY_RUN" = "1" ]; then
    log "  [DRY_RUN] NPUClusterPolicy 존재·$VENDOR.enabled·$NCP_DS·allocatable 확인"
    return 0
  fi

  if ! kubectl -n "$NAMESPACE" get npuclusterpolicy "$NCP" >/dev/null 2>&1; then
    log "BLOCKED: NPUClusterPolicy $NAMESPACE/$NCP 없음 — 데모 중단"
    BLOCKED=true; return 1
  fi
  ORIG_ENABLED="$(kubectl -n "$NAMESPACE" get npuclusterpolicy "$NCP" -o jsonpath="{.spec.$VENDOR.enabled}" 2>/dev/null || echo "")"
  local mode
  mode="$(kubectl -n "$NAMESPACE" get npuclusterpolicy "$NCP" -o jsonpath="{.spec.$VENDOR.managementMode}" 2>/dev/null || echo "")"
  log "  원값: $VENDOR.enabled=$ORIG_ENABLED managementMode='${mode:-<empty>}'"
  log "  시작: ncp_ds=$(ncp_ds_present && echo yes || echo no) ready=$(ds_ready "$NCP_DS_NS" "$NCP_DS") vendor_alloc=$(alloc_vendor) desc_alloc=$(alloc_desc)"

  if [ "$ORIG_ENABLED" != "true" ]; then
    log "BLOCKED: $VENDOR.enabled 이 true 가 아님(='$ORIG_ENABLED') — 켜진 벤더를 끄는 데모의 전제 불충족, 중단"
    BLOCKED=true; return 1
  fi
  if [ "$mode" = "external" ]; then
    log "BLOCKED: $VENDOR 가 external — descriptor 가 거절되므로 이 데모 대상이 아님, 중단"
    BLOCKED=true; return 1
  fi
}

# ── demo: 벤더 끄기 → descriptor 광고 → 소비 ──────────────────────────────
demo() {
  PHASE="demo"

  # 1) tenstorrent.enabled=false → 소유 device-plugin 회수(토글 off 삭제 경로).
  log "### demo 1: $VENDOR.enabled=false → NPUClusterPolicy 소유 device-plugin 삭제"
  ncp_patch "{\"spec\":{\"$VENDOR\":{\"enabled\":false}}}"
  NCP_PATCHED=true   # 이제부터 cleanup 이 원값으로 복원해야 한다
  if poll "$T_DS_DELETE" "$IV" ncp_ds_absent; then
    record "ncp_ds_deleted_on_disable" true "$NCP_DS_NS/$NCP_DS 삭제됨"
  else
    record "ncp_ds_deleted_on_disable" false "$T_DS_DELETE 초 내 $NCP_DS_NS/$NCP_DS 미삭제"
  fi

  # 2) descriptor 적용 → 생성된 device-plugin 이 뜨고 tenstorrent/blackhole-p150 광고.
  log "### demo 2: descriptor 적용 ($DESC_FILE) → 생성 device-plugin + 광고"
  kc apply -f "$DESC_FILE" >&2
  DESC_APPLIED=true
  if poll "$T_GEN_UP" "$IV" gen_ds_up; then
    record "generated_dp_created" true "$GEN_NS/$GEN_DS ready=$(ds_ready "$GEN_NS" "$GEN_DS")"
  else
    record "generated_dp_created" false "$T_GEN_UP 초 내 $GEN_NS/$GEN_DS 미기동(ready=$(ds_ready "$GEN_NS" "$GEN_DS"))"
  fi
  if [ "$DRY_RUN" = "1" ] || gen_advertised; then
    record "descriptor_resource_advertised" true "$RESOURCE_DESC=$(alloc_desc)"
  else
    record "descriptor_resource_advertised" false "$RESOURCE_DESC=$(alloc_desc)"
  fi

  # 3) 소비자 파드 → 스케줄 + /dev/tenstorrent 주입 확인.
  log "### demo 3: 소비자 파드 적용 ($CONSUMER_FILE) → 스케줄 + /dev 주입"
  kc apply -f "$CONSUMER_FILE" >&2
  CONSUMER_APPLIED=true
  if poll "$T_CONSUMER" "$IV" consumer_ready; then
    record "consumer_scheduled" true "phase=$(pod_phase)"
  else
    record "consumer_scheduled" false "$T_CONSUMER 초 내 Running/Succeeded 아님"
  fi
  local logs
  if [ "$DRY_RUN" = "1" ]; then
    log "+ kubectl -n $CONSUMER_NS logs $CONSUMER_NAME  (grep /dev/tenstorrent, not 'No such file')"
    record "consumer_saw_device" true "[DRY_RUN]"
  else
    logs="$(kubectl -n "$CONSUMER_NS" logs "$CONSUMER_NAME" 2>/dev/null || echo "")"
    if printf '%s' "$logs" | grep -q 'injected /dev/tenstorrent' && ! printf '%s' "$logs" | grep -q 'No such file'; then
      record "consumer_saw_device" true "/dev/tenstorrent 주입 확인"
    else
      record "consumer_saw_device" false "logs='$(printf '%s' "$logs" | tr '\n' ' ' | cut -c1-160)'"
    fi
  fi

  DEMO_DONE=true
}

# ── cleanup: 소비자·descriptor 제거 → GC → 벤더 재활성 → 복원(항상 실행) ───
cleanup() {
  [ "$CLEANED" = true ] && return 0
  CLEANED=true
  PHASE="cleanup"
  log "### cleanup: 소비자·descriptor 제거 → 생성물 GC → $VENDOR 재활성 복원"

  if [ "$CONSUMER_APPLIED" = true ]; then
    kc -n "$CONSUMER_NS" delete pod "$CONSUMER_NAME" --ignore-not-found >&2 || true
    CONSUMER_APPLIED=false
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

  # 벤더를 다시 켜는 복원은 우리가 실제로 disable 패치했을 때만 한다. preflight 가
  # BLOCKED 를 낸 경로(NCP 부재·enabled!=true·external)는 아무것도 바꾸지 않았으므로
  # 여기서 패치하면 의도적으로 꺼 둔 벤더를 켜 버린다 — 그 경로는 패치도 poll 도 하지 않는다.
  if [ "$NCP_PATCHED" = true ]; then
    # NCP_PATCHED 는 demo 가 돌았다는 뜻이고, demo 는 preflight 가 ORIG_ENABLED=true 를
    # 확인한 뒤에만 실행되므로 원값(true)으로 되돌린다.
    ncp_patch "{\"spec\":{\"$VENDOR\":{\"enabled\":${ORIG_ENABLED:-true}}}}" 2>/dev/null || \
      log "  경고: 복원 패치 실패 — 수동 확인 필요"
    if poll "$T_RESTORE" "$IV" ncp_ds_up; then
      record "restored_vendor_dp" true "ready=$(ds_ready "$NCP_DS_NS" "$NCP_DS") $RESOURCE_VENDOR=$(alloc_vendor)"
    else
      record "restored_vendor_dp" false "$T_RESTORE 초 내 미복원(ready=$(ds_ready "$NCP_DS_NS" "$NCP_DS") $RESOURCE_VENDOR=$(alloc_vendor))"
    fi
  else
    log "  복원 불필요 — NPUClusterPolicy 를 패치한 적 없음(패치·poll 생략)"
  fi
}

# ── finish: cleanup 보장 + JSON 요약 한 줄 + 종료코드 ─────────────────────
finish() {
  if [ "$CLEANED" != true ]; then cleanup || true; fi
  local verdict="FAIL" dry="false"
  if [ "$DRY_RUN" = "1" ]; then
    verdict="DRY"; dry="true"
  elif [ "$BLOCKED" = true ]; then
    verdict="BLOCKED"
  elif [ "$DEMO_DONE" = true ] && [ "$FAILS" -eq 0 ]; then
    verdict="PASS"
  fi
  printf '{"test":"new-vendor-by-descriptor","vendor":"%s","node":"%s","descriptor":"%s","resource":"%s","scenario_minutes":%s,"scenario_destructive":"%s","phase":"%s","checks":{%s},"fails":%s,"verdict":"%s","dryRun":%s,"date":"%s"}\n' \
    "$VENDOR" "$NODE" "$DESC_NAME" "$RESOURCE_DESC" "$SCENARIO_MINUTES" "$SCENARIO_DESTRUCTIVE" "$PHASE" "$CHECK_JSON" "$FAILS" "$verdict" "$dry" "$(utc_now)" \
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
if [ "$BLOCKED" != true ]; then
  demo
fi
# EXIT trap(finish) 이 cleanup 실행 + JSON 요약 + 종료코드를 담당한다.
