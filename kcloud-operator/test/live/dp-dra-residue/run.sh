#!/usr/bin/env bash
# ============================================================
# run.sh: device-plugin<->DRA 광고 주체 전환 왕복을 일으켜 잔재를 판정하는 하네스
# 상세: NPUClusterPolicy.spec.<vendor>.advertiseBy 를 devicePlugin↔dra 로 왕복시키며
#       snapshot.sh 로 상태를 남기고, 판정은 diff_snapshots 표를 보고 사람이 한다 — 잔재를
#       지우는 코드는 없다(PHASE·FORCE_FAIL_AT 세부는 case 문·maybe_force_fail() 참고).
# 용법: VENDOR=<rngd|tenstorrent> NODE=<node> [DRY_RUN=1] [PHASE=<phase>] run.sh
# 생성일: 2026-09-28 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

# 노드 SSH 계정. 소스에 박지 않는다 — 공개 저장소에 내부 계정이 실리면 안 된다.
# kubectl get/patch·kubectl-npu CLI·snapshot.sh(ssh ls/cat) 외의 쓰기는 하지 않는다.
SSH_USER="${SSH_USER:-ubuntu}"
export LC_ALL=C LANG=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "${SCRIPT_DIR}/../../.." && pwd)"
SNAPSHOT="${SCRIPT_DIR}/snapshot.sh"
KUBECTL_NPU="${REPO_DIR}/cmd/kubectl-npu/kubectl-npu"

# 기본 대상은 tenstorrent/<tenstorrent-node> 다 — 다른 후보
# (rngd/<rngd-node>.cluster.local)로 돌리려면 환경변수로 덮는다.
VENDOR="${VENDOR:-tenstorrent}"
NODE="${NODE:-k8s-worker3}"
DRY_RUN="${DRY_RUN:-0}"
PHASE="${PHASE:-all}"
SETTLE_TIMEOUT="${SETTLE_TIMEOUT:-300}"
SETTLE_AFTER_RETIRE="${SETTLE_AFTER_RETIRE:-60}"
RUN_ID="${RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
OUT_DIR="${SCRIPT_DIR}/snapshots/${RUN_ID}"
mkdir -p "$OUT_DIR"
# k8s 오브젝트 이름은 DNS-1123(소문자만) 이라 RUN_ID(대문자 T/Z 포함) 를 그대로 못 쓴다 —
# 시험 워크로드 이름에만 이 소문자판을 쓴다.
RUN_ID_LC="$(printf '%s' "$RUN_ID" | tr '[:upper:]' '[:lower:]')"

log() { printf '[%s] %s\n' "$(date -u +%T)" "$*" >&2; }

if [ "$#" -ne 0 ]; then
  echo "인자를 받지 않는다 — VENDOR/NODE/PHASE/DRY_RUN 환경변수로 지정" >&2
  exit 1
fi

# 노드 대상은 항상 kubectl get node "$NODE" 로 클러스터에 실재하는 노드에서만 IP 를
# 해석한다(snapshot.sh, kubelet-restart 단계 모두 동일) — 임의 주소를 지정할 방법이 없어
# 시범서비스 배포 노드에 접촉할 경로 자체가 없다. 존재하지 않는 위험을 막는 상수를 소스에
# 남기지 않는다(그 주소를 코드에 적는 것 자체가 피하려는 언급이 된다).

# vendor_json_path <vendor> — spec 안에서 이 벤더의 advertiseBy/dra 블록 jq 경로.
# 두 후보(rngd, tenstorrent)만 다룬다 — 나머지 벤더는 이번 실측 대상이
# 아니다(furiosa 는 rngd 와 스펙 구조가 달라 별도 분기가 필요해 추가하지 않는다).
vendor_json_path() {
  case "$1" in
    rngd) echo ".spec.furiosa.rngd" ;;
    tenstorrent) echo ".spec.tenstorrent" ;;
    *) echo "지원하지 않는 vendor: $1 (rngd|tenstorrent 만 이 하네스가 다룬다)" >&2; return 1 ;;
  esac
}

# NPUClusterPolicy 는 이 클러스터에 하나뿐이라는 전제는 pkg/npuctl/allocation.go 의 관례
# (다중 정책은 이론상 가능이나 첫 항목만 본다)와 같다. 조회는 그 값이 실제로 필요한 함수(쓰기
# 함수 전부)에서만 부른다 — summarize·diff 는 스냅샷 파일만 읽으므로 이 함수를 부르지 않고,
# 그래서 클러스터에 접속하지 않는다. 첫 호출 때 이 실행 시작
# 시점의 advertiseBy·dra.enabled 도 함께 잡아 둔다 — cleanup 이 실패 시 되돌릴 값이다.
NCP_NAME="" NCP_NS="" START_ADVERTISE="" START_DRA_ENABLED=""
resolve_ncp() {
  [ -n "$NCP_NAME" ] && return 0
  local json vpath
  json="$(kubectl get npuclusterpolicy -A -o json)"
  NCP_NAME="$(jq -r '.items[0].metadata.name // empty' <<<"$json")"
  NCP_NS="$(jq -r '.items[0].metadata.namespace // empty' <<<"$json")"
  if [ -z "$NCP_NAME" ]; then
    echo "NPUClusterPolicy 를 찾지 못했다" >&2
    exit 1
  fi
  vpath="$(vendor_json_path "$VENDOR")"
  START_ADVERTISE="$(jq -r ".items[0]${vpath}.advertiseBy // \"devicePlugin\"" <<<"$json")"
  START_DRA_ENABLED="$(jq -r ".items[0]${vpath}.dra.enabled // false" <<<"$json")"
}

ncp_json() { resolve_ncp; kubectl get npuclusterpolicy "$NCP_NAME" -n "$NCP_NS" -o json; }

# dra_patch_body <vendor> <true|false> — dra.enabled merge-patch 조각. set_dra_enabled 와
# cleanup 이 같이 쓴다(되돌릴 때도 같은 모양의 patch 라 중복을 안 둔다).
dra_patch_body() {
  case "$1" in
    rngd) echo "{\"spec\":{\"furiosa\":{\"rngd\":{\"dra\":{\"enabled\":${2}}}}}}" ;;
    tenstorrent) echo "{\"spec\":{\"tenstorrent\":{\"dra\":{\"enabled\":${2}}}}}" ;;
  esac
}

# maybe_force_fail <이름표> — FORCE_FAIL_AT 시험 훅. DRY_RUN 으로 cleanup(종료 처리기)이
# 실제로 되돌리기를 시도하는지 확인할 때만 쓴다.
maybe_force_fail() {
  if [ "${FORCE_FAIL_AT:-}" = "$1" ]; then
    echo "FORCE_FAIL_AT=$1 — 시험 훅으로 강제 실패" >&2
    return 1
  fi
}

# cleanup — trap EXIT 핸들러. 정상 종료(rc 0)면 아무것도 안 한다(정상 경로는 이미 되돌린
# 상태다). 비정상 종료면 이 실행이 바꾼 것만 되돌린다: 자기 시험 파드·템플릿 삭제,
# advertiseBy·dra.enabled 를 이 실행 시작 시점 값으로. 각 단계는 set +e 로 독립 시도한다 —
# 하나가 실패해도 나머지를 계속한다.
cleanup() {
  local rc=$?
  stop_dual_poll
  [ "$rc" -eq 0 ] && return 0
  set +e
  echo "비정상 종료(rc=${rc}) — 이 실행이 바꾼 것만 되돌린다" >&2

  local ns="default" pod body
  for pod in "dp-dra-residue-${RUN_ID_LC}-dra" "dp-dra-residue-${RUN_ID_LC}-dp"; do
    if [ "$DRY_RUN" = "1" ]; then
      echo "DRY_RUN(cleanup): kubectl delete -n $ns pod/$pod --ignore-not-found"
    else
      kubectl delete -n "$ns" "pod/$pod" --ignore-not-found >&2
    fi
  done
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN(cleanup): kubectl delete -n $ns resourceclaimtemplate/dp-dra-residue-${RUN_ID_LC}-claim --ignore-not-found"
  else
    kubectl delete -n "$ns" "resourceclaimtemplate/dp-dra-residue-${RUN_ID_LC}-claim" --ignore-not-found >&2
  fi

  if [ -n "$START_ADVERTISE" ]; then
    if [ "$DRY_RUN" = "1" ]; then
      echo "DRY_RUN(cleanup): $KUBECTL_NPU allocation switch --vendor $VENDOR --to $START_ADVERTISE --force"
    else
      "$KUBECTL_NPU" allocation switch --vendor "$VENDOR" --to "$START_ADVERTISE" --force >&2
    fi
    echo "되돌림: advertiseBy -> $START_ADVERTISE" >&2
  fi

  if [ -n "$START_DRA_ENABLED" ] && [ -n "$NCP_NAME" ]; then
    body="$(dra_patch_body "$VENDOR" "$START_DRA_ENABLED")"
    if [ "$DRY_RUN" = "1" ]; then
      echo "DRY_RUN(cleanup): kubectl patch npuclusterpolicy $NCP_NAME -n $NCP_NS --type=merge -p '${body}'"
    else
      kubectl patch npuclusterpolicy "$NCP_NAME" -n "$NCP_NS" --type=merge -p "$body" >&2
    fi
    echo "되돌림: dra.enabled -> $START_DRA_ENABLED" >&2
  fi
}
trap cleanup EXIT
# 신호로 끊겨도 EXIT 트랩(정리)이 돌도록 신호를 종료 코드로 바꾼다(128+신호 번호) —
# 트랩이 없는 SIGTERM·SIGHUP 은 셸을 바로 끝내 EXIT 트랩이 돌지 않는다.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# resource_name — 안전 게이트가 셀 확장 자원명(spec.<vendor>.resourceName, rngd 는
# furiosa.ai/rngd 접두 그대로, tenstorrent 는 resourcePrefix 가 없어 resourceName 이 곧
# 전체 키다).
resource_name() {
  local path; path="$(vendor_json_path "$VENDOR")"
  ncp_json | jq -r "${path}.resourceName"
}

# pod_count_using_resource <node> <resource> — 그 노드에서 그 확장 자원을 requests 나
# limits 에 담은 파드 수(device-reset/run.sh 와 같은 방식 — 확장 자원은 k8s 가 requests
# 를 limits 에서 자동 복사하지만 어느 쪽으로 선언했든 잡는다).
# unique_by(.metadata.uid) 로 다중 컨테이너가 같은 자원을 각각 요청해도 파드 하나를 두 번
# 세지 않는다.
pod_count_using_resource() {
  local node="$1" resource="$2"
  kubectl get pods -A --field-selector "spec.nodeName=${node}" -o json | \
    jq --arg r "$resource" \
      '[.items[] | select(.spec.containers[]? |
        ((.resources.requests // {}) | has($r)) or ((.resources.limits // {}) | has($r)))] | unique_by(.metadata.uid) | length'
}

# safety_gate — 이 노드에서 이 벤더 자원을 요구하는 파드가 0 인지 확인한다. 0 이 아니면
# 멈춘다 — 그 어떤 write 함수도 이 확인 없이 kubectl patch/switch/kubelet 재시작을 하지 않는다.
safety_gate() {
  local resource count
  # resolve_ncp 를 파이프/명령치환(subshell) 안이 아니라 여기서 직접 부른다 — subshell 안에서
  # 부르면 NCP_NAME/NCP_NS 갱신이 그 subshell 을 못 벗어나 set_dra_enabled 등 부모 스코프의
  # 직접 참조가 빈 값을 본다.
  resolve_ncp
  resource="$(resource_name)"
  count="$(pod_count_using_resource "$NODE" "$resource")"
  log "안전 게이트: node=$NODE resource=$resource pod_count=$count"
  if [ "$count" -ne 0 ]; then
    echo "안전 게이트 실패: $NODE 에 $resource 를 요구하는 파드 ${count}개 — 중단" >&2
    kubectl get pods -A --field-selector "spec.nodeName=${NODE}" -o json | \
      jq --arg r "$resource" -r \
        '.items[] | select(.spec.containers[]? |
          ((.resources.requests // {}) | has($r)) or ((.resources.limits // {}) | has($r))) |
          .metadata.namespace + "/" + .metadata.name' >&2
    return 1
  fi
}

# set_dra_enabled <true|false> — DRA 드라이버 배포 여부(advertiseBy 와는 독립적인 별도 설정,
# DRASpec 참고)를 patch. merge-patch 라 dra 블록의 image 등 다른 필드는 그대로 둔다.
set_dra_enabled() {
  local val="$1" body
  safety_gate
  maybe_force_fail "set_dra_enabled(${val})" || return 1
  body="$(dra_patch_body "$VENDOR" "$val")"
  log "dra.enabled=${val} patch: ${body}"
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: kubectl patch npuclusterpolicy $NCP_NAME -n $NCP_NS --type=merge -p '${body}'"
    return 0
  fi
  kubectl patch npuclusterpolicy "$NCP_NAME" -n "$NCP_NS" --type=merge -p "$body"
}

# switch_advertise <to> — kubectl-npu CLI 로 advertiseBy 를 바꾸고 즉시 실제 spec 값으로
# 교차 확인한다. CLI 의 성공 출력줄만 믿지 않는다(이 CLI 의 첫 라이브 사용).
switch_advertise() {
  local to="$1" path actual
  safety_gate
  maybe_force_fail "switch_advertise(${to})" || return 1
  path="$(vendor_json_path "$VENDOR")"
  log "advertiseBy 전환 요청: vendor=$VENDOR to=$to"
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: $KUBECTL_NPU allocation switch --vendor $VENDOR --to $to --force"
    return 0
  fi
  "$KUBECTL_NPU" allocation switch --vendor "$VENDOR" --to "$to" --force
  actual="$(ncp_json | jq -r "${path}.advertiseBy // \"devicePlugin\"")"
  log "교차 확인(kubectl get npuclusterpolicy): ${path}.advertiseBy = ${actual}"
  if [ "$actual" != "$to" ]; then
    echo "교차 확인 실패: CLI 는 성공을 말했지만 spec 은 ${actual} — 중단" >&2
    return 1
  fi
}

# wait_driver_publishing <timeout초> — set_dra_enabled true 로 막 올라온 DRA 드라이버
# DaemonSet 이 이 노드에 ResourceSlice 를 최초로 발행할 때까지 5초 간격으로 기다린다.
# switch_advertise dra 는 refuseAdvertiseSwitch 가 그 발행을 이미 확인해야 통과시키는데,
# DaemonSet 콜드스타트(스케줄+이미지풀+드라이버 초기화)는 수 초가 걸려 곧바로 전환을
# 요청하면 매번 거절된다(wait_settle 은 첫 거절 사유에서 바로
# 멈추고 재시도하지 않는다) — set_dra_enabled true 뒤 switch_advertise dra 전에 부른다.
wait_driver_publishing() {
  local timeout="$1" elapsed=0 count
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: wait_driver_publishing 생략(드라이버를 실제로 올리지 않았다)"
    return 0
  fi
  while [ "$elapsed" -lt "$timeout" ]; do
    count="$(kubectl get resourceslices.resource.k8s.io -o json 2>/dev/null | \
      jq --arg n "$NODE" '[.items[]? | select(.spec.nodeName==$n)] | length' 2>/dev/null || echo 0)"
    log "드라이버 발행 폴링(${elapsed}s): ${NODE} 의 ResourceSlice 개수=${count:-0}"
    if [ "${count:-0}" -gt 0 ]; then
      log "드라이버 발행 확인 시각: $(date -u +%FT%TZ)"
      return 0
    fi
    sleep 5
    elapsed=$((elapsed + 5))
  done
  echo "드라이버가 ${timeout}초 안에 ResourceSlice 를 발행하지 않음" >&2
  return 1
}

# wait_settle <to> — 컨트롤러가 노드별 실제 전환을 끝낼 때까지 기다린다(상한 SETTLE_TIMEOUT초,
# 기본 300). status.advertiseSwitch 의 이 vendor/node 항목과 dra-owned 라벨을 함께 봐야
# 거절된 상태를 "아직 진행 중"으로 착각하지 않는다.
wait_settle() {
  local to="$1" label="kcloud.ai/${VENDOR}.dra-owned" elapsed=0 interval=10
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: wait_settle $to 생략(전환을 안 했으므로 기다릴 것이 없다)"
    return 0
  fi
  resolve_ncp  # 직접 호출 — 안 하면 아래 루프의 ncp_json 이 매 폴링마다 다시 조회한다
  while [ "$elapsed" -lt "$SETTLE_TIMEOUT" ]; do
    local applied reason has_label switch_json
    switch_json="$(ncp_json | jq -c --arg v "$VENDOR" --arg n "$NODE" \
      '[.status.advertiseSwitch[]? | select(.vendor==$v and .node==$n)][0] // {}')"
    applied="$(jq -r '.applied // empty' <<<"$switch_json")"
    reason="$(jq -r '.reason // empty' <<<"$switch_json")"
    has_label="$(kubectl get node "$NODE" -o json | jq -r --arg k "$label" '.metadata.labels[$k] // empty')"
    log "폴링(${elapsed}s): applied=${applied:-<none>} reason=${reason:-<none>} dra-owned=${has_label:-<none>}"
    if [ "$to" = "dra" ]; then
      if [ -n "$reason" ]; then
        echo "전환 거절: $reason" >&2
        return 1
      fi
      if [ "$applied" = "true" ] && [ "$has_label" = "true" ]; then
        log "정착 확인: dra 로 전환 완료"
        return 0
      fi
    else
      if [ -z "$has_label" ]; then
        log "정착 확인: dra-owned 라벨 회수 완료"
        return 0
      fi
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done
  echo "정착 대기 시간 초과(${SETTLE_TIMEOUT}s) — 마지막 상태는 위 폴링 로그 참고" >&2
  return 1
}

# dual_advertisement_condition — 현재 NPUClusterPolicy 의 DualAdvertisement 컨디션 객체를
# 한 줄 JSON 으로 낸다(없으면 "{}"). 다른 조회 함수처럼 DRY_RUN 에서도 실제로 읽는다(읽기
# 전용 — resolve_ncp/ncp_json 과 같은 원칙).
dual_advertisement_condition() {
  ncp_json | jq -c '[.status.conditions[]? | select(.type=="DualAdvertisement")][0] // {}'
}

# wait_retired_label <timeout초> — kcloud.ai/<vendor>.dra-retired=true 라벨이 노드에 붙을
# 때까지 5초 간격으로 기다리고, 붙은 시각(UTC)을 로그에 남긴다. switch devicePlugin 뒤
# wait_settle(dra-owned 회수 확인) 다음에 부른다 — 두 라벨은 별도 patch 라(reconcileDRARetiredLabel)
# 같은 reconcile 에 함께 바뀌어도 순서상 뒤따라올 수 있어 따로 본다.
wait_retired_label() {
  local timeout="$1" key="kcloud.ai/${VENDOR}.dra-retired" elapsed=0 val
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: wait_retired_label 생략(전환을 안 했으므로 기다릴 것이 없다)"
    return 0
  fi
  while [ "$elapsed" -lt "$timeout" ]; do
    val="$(kubectl get node "$NODE" -o json | jq -r --arg k "$key" '.metadata.labels[$k] // empty')"
    log "retired 라벨 폴링(${elapsed}s): ${key}=${val:-<none>}"
    if [ "$val" = "true" ]; then
      log "retired 라벨 확인 시각: $(date -u +%FT%TZ)"
      return 0
    fi
    sleep 5
    elapsed=$((elapsed + 5))
  done
  echo "retired 라벨이 ${timeout}초 안에 나타나지 않음" >&2
  return 1
}

# start_dual_poll/stop_dual_poll <logfile> — switch devicePlugin 부터 after-dp 스냅샷까지
# DualAdvertisement 컨디션을 5초 간격 백그라운드로 기록해 True/False 전이 시각을 남긴다.
# DUAL_POLL_PID 는 cleanup() 이 비정상 종료 시에도 이 프로세스를 정리할 수 있게 둔다.
DUAL_POLL_PID=""
start_dual_poll() {
  local logfile="$1"
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: dual-condition 폴링 생략"
    return 0
  fi
  : > "$logfile"
  ( while true; do
      printf '%s %s\n' "$(date -u +%FT%TZ)" "$(dual_advertisement_condition)"
      sleep 5
    done
  ) >> "$logfile" 2>&1 &
  DUAL_POLL_PID=$!
  log "dual-condition 폴링 시작(PID $DUAL_POLL_PID) -> $logfile"
}

stop_dual_poll() {
  if [ -n "$DUAL_POLL_PID" ]; then
    kill "$DUAL_POLL_PID" 2>/dev/null || true
    wait "$DUAL_POLL_PID" 2>/dev/null || true
    log "dual-condition 폴링 종료(PID $DUAL_POLL_PID)"
    DUAL_POLL_PID=""
  fi
}

# take_snapshot <label> — snapshot.sh 로 노드 스냅샷을 남기고, 그 시점의 DualAdvertisement
# 컨디션도 같은 디렉터리에 dual-advertisement-condition.json 으로 함께 남긴다.
take_snapshot() {
  local label="$1"
  log "스냅샷: $label -> $OUT_DIR/$label"
  "$SNAPSHOT" "$OUT_DIR/$label" "$NODE" "$VENDOR"
  dual_advertisement_condition > "$OUT_DIR/$label/dual-advertisement-condition.json"
}

# diff_snapshots <label1> <label2> — 두 스냅샷 사이 핵심 필드의 차이를 사람이 읽을 표로 낸다.
# 판정(값 0 등록 잔재/살아 있는 광고/고아 객체, 해로움 여부)은 이 표를 보고 보고서에서 한다 —
# 여기서 자동으로 내리지 않는다(지우는 코드도 없다).
diff_snapshots() {
  local l1="$1" l2="$2" a="$OUT_DIR/$1" b="$OUT_DIR/$2"
  echo "### ${l1} -> ${l2}"
  echo "-- node-allocatable.allocatable --"
  diff -u <(jq -S '.allocatable' "$a/node-allocatable.json") <(jq -S '.allocatable' "$b/node-allocatable.json") || true
  echo "-- node-labels.labels --"
  diff -u <(jq -S '.labels' "$a/node-labels.json") <(jq -S '.labels' "$b/node-labels.json") || true
  echo "-- resourceslices(이 노드, spec.nodeName=$NODE) --"
  diff -u <(jq -S --arg n "$NODE" '[.items[] | select(.spec.nodeName==$n)]' "$a/resourceslices.json") \
          <(jq -S --arg n "$NODE" '[.items[] | select(.spec.nodeName==$n)]' "$b/resourceslices.json") || true
  echo "-- 이 노드의 파드(이름/이미지/phase) --"
  diff -u <(jq -S '[.items[] | {name:.metadata.name, image:(.spec.containers[0].image), phase:.status.phase}] | sort' "$a/pods.json") \
          <(jq -S '[.items[] | {name:.metadata.name, image:(.spec.containers[0].image), phase:.status.phase}] | sort' "$b/pods.json") || true
  echo "-- npuclusterpolicy status.advertiseSwitch(이 vendor) --"
  diff -u <(jq -S --arg v "$VENDOR" '[.items[0].status.advertiseSwitch[]? | select(.vendor==$v)]' "$a/npuclusterpolicy.json") \
          <(jq -S --arg v "$VENDOR" '[.items[0].status.advertiseSwitch[]? | select(.vendor==$v)]' "$b/npuclusterpolicy.json") || true
  echo "-- SSH 관측 파일 상태(manifest, partial 은 판정 불가로 별도 표시) --"
  diff -u <(grep -E "cdi-files|dev-nodes|kubelet-sockets" "$a/manifest.txt") \
          <(grep -E "cdi-files|dev-nodes|kubelet-sockets" "$b/manifest.txt") || true
  for f in cdi-files dev-nodes kubelet-sockets; do
    if grep -q "^${f}\.txt\s\+partial(" "$a/manifest.txt" "$b/manifest.txt" 2>/dev/null; then
      echo "판정 불가(${f}.txt): 전 또는 후 스냅샷이 partial — diff \"변화 없음\"을 그대로 믿지 않는다"
    fi
  done
}

# summarize_snapshot <label> — 스냅샷 디렉터리의 파일만 읽어(클러스터 미접속) 기계가 낸 값
# 한 줄을 표준출력에 낸다. evidence.sh 의 --expected 를 사람이 손으로 조립하지 않고 이 줄을
# 그대로 복사해 쓰기 위한 것이다.
summarize_snapshot() {
  local label="$1" dir="$OUT_DIR/$1" vpath rname alloc rs_count dc_count adv dra_en dual retired retired_key
  vpath="$(vendor_json_path "$VENDOR")"
  # 스냅샷 파일의 npuclusterpolicy.json 은 snapshot.sh 의 kget 이 낸 {collected,items} 포맷이라
  # ncp_json()(단일 오브젝트) 과 달리 .items[0] 을 한 겹 더 거친다.
  rname="$(jq -r ".items[0]${vpath}.resourceName" "$dir/npuclusterpolicy.json")"
  alloc="$(jq -r --arg r "$rname" '.allocatable[$r] // "0"' "$dir/node-allocatable.json")"
  rs_count="$(jq --arg n "$NODE" '[.items[] | select(.spec.nodeName==$n)] | length' "$dir/resourceslices.json")"
  dc_count="$(jq '.items | length' "$dir/deviceclasses.json")"
  adv="$(jq -r ".items[0]${vpath}.advertiseBy // \"unset\"" "$dir/npuclusterpolicy.json")"
  dra_en="$(jq -r ".items[0]${vpath}.dra.enabled // false" "$dir/npuclusterpolicy.json")"
  # dual/retired: take_snapshot 이 함께 남긴 dual-advertisement-condition.json·node-labels.json
  # 만 읽는다(요청 필드, 라이브 확인 절) — summarize 는 클러스터에 접속하지 않는다.
  dual="$(jq -r '.status // "unset"' "$dir/dual-advertisement-condition.json" 2>/dev/null || echo unset)"
  retired_key="kcloud.ai/${VENDOR}.dra-retired"
  retired="$(jq -r --arg k "$retired_key" '.labels[$k] // "absent"' "$dir/node-labels.json")"
  printf '%s allocatable[%s]=%s resourceslices(node)=%s deviceclasses=%s advertiseBy=%s dra.enabled=%s dual=%s retired=%s\n' \
    "$label" "$rname" "$alloc" "$rs_count" "$dc_count" "$adv" "$dra_en" "$dual" "$retired"
}

# pick_test_image — 이 노드에 이미 떠 있는 kube-system 표준 파드 중 셸(sh)과 ls 가 둘 다
# 있는 이미지를 exec 로 확인해 고른다 — 새 이미지를 pull 하지 않는다.
# ls 까지 확인하는 이유: kube-proxy 는 sh 는 있으나 ls 가 없어 "sh: ls: not found" 로
# 장치 목록 확인 명령 자체가 실패하는 경우가 있다 — sh 만 확인하면 이 실패를 못 잡는다.
pick_test_image() {
  local name pod img
  for name in nginx-proxy calico-node kube-proxy nodelocaldns; do
    pod="$(kubectl get pods -n kube-system --field-selector "spec.nodeName=${NODE}" -o json | \
      jq -r --arg p "$name" '.items[] | select(.metadata.name | startswith($p)) | .metadata.name' | head -1)"
    [ -z "$pod" ] && continue
    if kubectl exec -n kube-system "$pod" -c "$name" -- sh -c 'command -v ls' >/dev/null 2>&1; then
      img="$(kubectl get pod "$pod" -n kube-system -o jsonpath='{.spec.containers[0].image}')"
      echo "$img"
      return 0
    fi
  done
  return 1
}

# apply_test_manifest <템플릿파일> <이미지> — __RUN_ID__/__NODE__/__TEST_IMAGE__ 를 치환해
# 적용한다. DRY_RUN 이면 치환 결과만 찍는다.
apply_test_manifest() {
  local template="$1" image="$2" rendered
  rendered="$(sed -e "s/__RUN_ID__/${RUN_ID_LC}/g" -e "s/__NODE__/${NODE}/g" -e "s#__TEST_IMAGE__#${image}#g" "$template")"
  if [ "$DRY_RUN" = "1" ]; then
    printf 'DRY_RUN: kubectl apply -f -\n%s\n' "$rendered"
    return 0
  fi
  printf '%s\n' "$rendered" | kubectl apply -f -
}

# wait_pod_running <ns> <pod> <timeout초> — Running 이 될 때까지 5초 간격 폴링. 실패하면
# 상태·이벤트를 남기고 return 1(호출부가 그래도 계속 진행 — "이것도 결과"다).
wait_pod_running() {
  local ns="$1" pod="$2" timeout="$3" elapsed=0 phase
  while [ "$elapsed" -lt "$timeout" ]; do
    phase="$(kubectl get pod "$pod" -n "$ns" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
    log "시험 파드 폴링(${elapsed}s): $pod phase=${phase:-<none>}"
    [ "$phase" = "Running" ] && return 0
    sleep 5
    elapsed=$((elapsed + 5))
  done
  echo "시험 파드 $pod 가 ${timeout}초 안에 Running 되지 않음 — 상태·이벤트를 남긴다" >&2
  kubectl get pod "$pod" -n "$ns" -o yaml >&2 || true
  kubectl get events -n "$ns" --field-selector "involvedObject.name=${pod}" >&2 || true
  return 1
}

# cleanup_test_resource <ns> <오브젝트...> — 이 실행(RUN_ID)이 만든 시험 자원만 지운다.
# "지우는 코드 금지" 는 잔재를 지우는 코드를 뜻한다 — 자기 시험 자원 정리는 별개다.
cleanup_test_resource() {
  local ns="$1"; shift
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: kubectl delete -n $ns $* --ignore-not-found"
    return 0
  fi
  kubectl delete -n "$ns" "$@" --ignore-not-found --wait=true
}

# workload_dra — DRA ResourceClaim 으로 장치를 실제로 파드에 주입해 보인다.
# 확인 뒤 만든 자원을 지우고 안전 게이트가 0 으로 돌아왔는지 본다.
workload_dra() {
  local ns="default" claim="dp-dra-residue-${RUN_ID_LC}-claim" pod="dp-dra-residue-${RUN_ID_LC}-dra" image
  if [ "$DRY_RUN" = "1" ]; then
    apply_test_manifest "${SCRIPT_DIR}/manifests/dra-claim.yaml" "(라이브에서 pick_test_image 로 결정)"
    echo "DRY_RUN: 파드 대기·로그 확인·삭제 생략"
    return 0
  fi
  image="$(pick_test_image)" || { echo "셸 있는 이미지를 못 찾아 workload-dra 를 건너뛴다" >&2; return 1; }
  log "시험 이미지(workload-dra): $image"
  apply_test_manifest "${SCRIPT_DIR}/manifests/dra-claim.yaml" "$image"
  if wait_pod_running "$ns" "$pod" 300; then
    log "주입 확인 로그(workload-dra):"
    kubectl logs "$pod" -n "$ns" | tee "$OUT_DIR/workload-dra.log" >&2
    kubectl get resourceclaim -n "$ns" -o yaml > "$OUT_DIR/workload-dra-resourceclaim.yaml"
  fi
  cleanup_test_resource "$ns" "pod/$pod" "resourceclaimtemplate/$claim"
  safety_gate
}

# workload_dp — 같은 확인을 devicePlugin(고전 확장 자원) 경로로 반복한다.
workload_dp() {
  local ns="default" pod="dp-dra-residue-${RUN_ID_LC}-dp" image
  if [ "$DRY_RUN" = "1" ]; then
    apply_test_manifest "${SCRIPT_DIR}/manifests/dp-pod.yaml" "(라이브에서 pick_test_image 로 결정)"
    echo "DRY_RUN: 파드 대기·로그 확인·삭제 생략"
    return 0
  fi
  image="$(pick_test_image)" || { echo "셸 있는 이미지를 못 찾아 workload-dp 를 건너뛴다" >&2; return 1; }
  log "시험 이미지(workload-dp): $image"
  apply_test_manifest "${SCRIPT_DIR}/manifests/dp-pod.yaml" "$image"
  if wait_pod_running "$ns" "$pod" 300; then
    log "주입 확인 로그(workload-dp):"
    kubectl logs "$pod" -n "$ns" | tee "$OUT_DIR/workload-dp.log" >&2
  fi
  cleanup_test_resource "$ns" "pod/$pod"
  safety_gate
}

# poll_resourceslice_gone — dra.enabled=false 직후부터 5초 간격, 최대 5분 ResourceSlice
# 개수를 폴링해 시각과 개수를 로그에 남긴다(고아 소멸 시각의 근거).
# kubectl 실패는 0 개와 다르다 — 실패를 0 으로 읽으면 "관측 못함"이 "사라짐"으로 둔갑한다.
# rc 를 grep 과 분리해 실패는 count=error 로 남기고 0 판정에 안 쓴다.
poll_resourceslice_gone() {
  local logfile="$OUT_DIR/resourceslice-poll.log" elapsed=0 count ts raw kubectl_rc ever_observed=0
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: poll_resourceslice_gone 생략"
    return 0
  fi
  : > "$logfile"
  while [ "$elapsed" -le 300 ]; do
    ts="$(date -u +%FT%TZ)"
    if raw="$(kubectl get resourceslices.resource.k8s.io --no-headers 2>/dev/null)"; then
      kubectl_rc=0
    else
      kubectl_rc=$?
    fi
    if [ "$kubectl_rc" -ne 0 ]; then
      printf '%s elapsed=%ss count=error rc=%s\n' "$ts" "$elapsed" "$kubectl_rc" | tee -a "$logfile" >&2
    else
      ever_observed=1
      count="$(grep -c . <<<"$raw" || true)"
      printf '%s elapsed=%ss count=%s\n' "$ts" "$elapsed" "$count" | tee -a "$logfile" >&2
      if [ "$count" -eq 0 ]; then
        log "ResourceSlice 0 확인 — 폴링 종료"
        return 0
      fi
    fi
    sleep 5
    elapsed=$((elapsed + 5))
  done
  if [ "$ever_observed" -eq 0 ]; then
    echo "unobservable — 5분 내내 kubectl 오류만 있어 개수를 한 번도 못 봄, $logfile 참고" | tee -a "$logfile" >&2
    return 1
  fi
  echo "ResourceSlice 가 5분 안에 0 이 되지 않음 — $logfile 참고" >&2
}

# dra_daemonset_name — 이 벤더의 DRA 드라이버 DaemonSet 이름(furiosa·rngd 는 같은
# DaemonSet 을 공유한다 — dra_driver.go 의 draDSNameFuriosa/draDSNameTenstorrent 와 동일).
dra_daemonset_name() {
  case "$VENDOR" in
    rngd) echo "kcloud-furiosa-dra-driver" ;;
    tenstorrent) echo "kcloud-tenstorrent-dra-driver" ;;
  esac
}

# poll_teardown — dra.enabled=false 직후부터 2초 간격, 최대 60초 DRA DaemonSet 존재
# 여부·이 노드의 DRA 파드 수·retired 라벨 존재 여부를 함께 teardown-poll.log 에 남긴다
# (기대 동작: retired 는 DaemonSet 이 사라진 뒤에만 없어지고, 그 사이 이 노드에 새 DRA
# 파드가 뜨지 않는다 — 판정은 이 로그를 보고 사람이 한다).
poll_teardown() {
  local logfile="$OUT_DIR/teardown-poll.log" ds retired_key elapsed=0 ds_present pod_count retired ts
  ds="$(dra_daemonset_name)"
  retired_key="kcloud.ai/${VENDOR}.dra-retired"
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: poll_teardown 생략"
    return 0
  fi
  : > "$logfile"
  while [ "$elapsed" -le 60 ]; do
    ts="$(date -u +%FT%TZ)"
    if kubectl get daemonset -n kcloud "$ds" >/dev/null 2>&1; then
      ds_present=true
    else
      ds_present=false
    fi
    pod_count="$(kubectl get pods -n kcloud --field-selector "spec.nodeName=${NODE}" -o json | \
      jq --arg d "$ds" '[.items[] | select(.metadata.name | startswith($d))] | length')"
    retired="$(kubectl get node "$NODE" -o json | jq -r --arg k "$retired_key" '.metadata.labels[$k] // "absent"')"
    printf '%s daemonset_present=%s node_dra_pods=%s retired=%s\n' \
      "$ts" "$ds_present" "${pod_count:-0}" "$retired" | tee -a "$logfile" >&2
    if [ "$ds_present" = "false" ] && [ "$retired" = "absent" ]; then
      log "teardown 완료 확인(daemonset·retired 라벨 모두 소거) — 폴링 종료"
      return 0
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done
  log "teardown-poll 60초 종료 — 마지막 상태는 $logfile 참고"
}

main() {
  safety_gate
  take_snapshot before

  set_dra_enabled true
  wait_driver_publishing 90
  switch_advertise dra
  wait_settle dra
  take_snapshot after-dra
  workload_dra

  switch_advertise devicePlugin
  start_dual_poll "$OUT_DIR/dual-condition-poll.log"
  wait_settle devicePlugin
  wait_retired_label 120
  if [ "$DRY_RUN" = "1" ]; then
    echo "DRY_RUN: SETTLE_AFTER_RETIRE(${SETTLE_AFTER_RETIRE}s) 대기 생략"
  else
    log "SETTLE_AFTER_RETIRE(${SETTLE_AFTER_RETIRE}s) 대기 시작"
    sleep "$SETTLE_AFTER_RETIRE"
  fi
  take_snapshot after-dp
  stop_dual_poll
  workload_dp
  poll_resourceslice_gone

  set_dra_enabled false
  poll_teardown
  take_snapshot restored

  diff_snapshots before after-dra
  diff_snapshots after-dra after-dp
  diff_snapshots before restored

  log "요약: 스냅샷 before/after-dra/after-dp/restored 는 $OUT_DIR."
  log "kubelet 재시작은 restored 에 값 0 등록 잔재가 있을 때만 PHASE=kubelet-restart 로."
}

case "$PHASE" in
  safety-gate) safety_gate ;;
  snapshot) take_snapshot "${LABEL:?LABEL 필요}" ;;
  summarize) summarize_snapshot "${LABEL:?LABEL 필요}" ;;
  enable-dra) set_dra_enabled true ;;
  disable-dra) set_dra_enabled false ;;
  wait-driver-publishing) wait_driver_publishing 90 ;;
  switch-to-dra) switch_advertise dra ;;
  switch-to-dp) switch_advertise devicePlugin ;;
  wait-dra) wait_settle dra ;;
  wait-dp) wait_settle devicePlugin ;;
  wait-retired) wait_retired_label 120 ;;
  workload-dra) workload_dra ;;
  workload-dp) workload_dp ;;
  resourceslice-poll) poll_resourceslice_gone ;;
  teardown-poll) poll_teardown ;;
  diff) diff_snapshots "${LABEL_A:?LABEL_A 필요}" "${LABEL_B:?LABEL_B 필요}" ;;
  kubelet-restart)
    safety_gate
    NODE_IP="$(kubectl get node "$NODE" -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}')"
    if [ "$DRY_RUN" = "1" ]; then
      echo "DRY_RUN: ssh ${SSH_USER}@${NODE_IP} sudo systemctl restart kubelet"
    else
      log "kubelet 재시작: $NODE ($NODE_IP)"
      ssh -o BatchMode=yes -o ConnectTimeout=6 "${SSH_USER}@${NODE_IP}" 'sudo systemctl restart kubelet'
    fi
    ;;
  all) main ;;
  *) echo "알 수 없는 PHASE: $PHASE" >&2; exit 1 ;;
esac
