#!/usr/bin/env bash
# ============================================================
# run.sh: Rebellions ATOM 격리 네 항목(연산·용량·대역폭·장애) 라이브 실측 하네스(<rebellions-node>)
# 상세: preflight→measure(ssh 로 measure-node.sh 실행)→verdict→cleanup 순서로 돈다. PHASE=verdict
#       RUN_DIR=<dir> 로 부르면 라이브 접근 없이 저장된 json 만 읽어 ISOLATION 줄만 낸다.
# 생성일: 2026-09-28 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DRY_RUN="${DRY_RUN:-0}"
KCTX="${KUBECTL_CONTEXT:-kubernetes-admin@cluster.local}"

VENDOR="${VENDOR:-rebellions}"
NODE="${NODE:-node5}"
NODE_SSH="${NODE_SSH:?NODE_SSH 을 지정하십시오}"
SERVICE="${SERVICE:-atomplus-vllm.service}"

WORKLOAD_FILE="${WORKLOAD_FILE:-$SCRIPT_DIR/workload.py}"
MEASURE_FILE="${MEASURE_FILE:-$SCRIPT_DIR/measure-node.sh}"

DEV0="${DEV0:-0}"
DEV1="${DEV1:-1}"
DUR="${DUR:-20}"
BARRIER="${BARRIER:-40}"
CAP_DUR="${CAP_DUR:-15}"
CAP_BARRIER="${CAP_BARRIER:-60}"
CAP_K="${CAP_K:-32}"
CAP_DIM="${CAP_DIM:-2048}"
BW_DIM="${BW_DIM:-4096}"
MATMUL_DIM="${MATMUL_DIM:-2048}"
FAULT_DUR="${FAULT_DUR:-30}"
FREE_WAIT="${FREE_WAIT:-90}"
MIN_FREE_GIB="${MIN_FREE_GIB:-5}"

RUN_ID="${RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)-$$}"
RUN_DIR="${RUN_DIR:-$SCRIPT_DIR/.run}"
REMOTE_OUT="${REMOTE_OUT:-/home/kcloud/rebellions-isolation-${RUN_ID}}"
T_MEASURE="${T_MEASURE:-900}"

mkdir -p "$RUN_DIR"

utc_now() { date -u +%FT%TZ; }
log() { printf '%s\n' "$*" >&2; }
kubeget() { if [ "$DRY_RUN" = "1" ]; then return 0; fi; kubectl --context "$KCTX" "$@"; }
node_ssh() { ssh -o BatchMode=yes -o ConnectTimeout=15 "$NODE_SSH" "$@"; }

FAILS=0
BLOCKED=false
CLASSIFICATION=""
BLOCK_REASON=""
PHASE_STATE="init"
RUN_DONE=false
CLEANED=false
SVC_BEFORE=""
SVC_FINAL=""
ISOLATION_LINE=""

record() {
  local name="$1" ok="$2" detail="${3:-}"
  if [ "$ok" != "true" ]; then FAILS=$((FAILS + 1)); fi
  local tag; tag=$([ "$ok" = "true" ] && echo PASS || echo FAIL)
  log "  [$tag] $name ${detail:+— $detail}"
}

set_blocked() {
  BLOCKED=true
  CLASSIFICATION="$1"
  BLOCK_REASON="$2"
  log "BLOCKED[$1]: $2"
}

# isolation_verdict_line RUN_DIR — 항목별 *.json(run files) 만 읽어 ISOLATION: 한 줄을 stdout 에
# 찍고 verdict.json 을 RUN_DIR 에 남긴다. 라이브 접근(kubectl/ssh) 없음 — PHASE=verdict 전용
# 경로와 라이브 측정 뒤 경로가 이 함수 하나를 공유한다(판정 로직 중복 방지).
isolation_verdict_line() {
  local dir="$1"
  python3 - "$dir" <<'PY'
import json, os, sys

d = sys.argv[1]


def load(name):
    try:
        return json.load(open(os.path.join(d, name)))
    except Exception:
        return {}


compute_solo = load("compute-solo.json")
compute_d0 = load("compute-d0.json")
capacity_solo = load("capacity-solo.json")
capacity_d0 = load("capacity-d0.json")
bandwidth_solo = load("bandwidth-solo.json")
bandwidth_d0 = load("bandwidth-d0.json")
fault_d0 = load("fault-d0.json")
fault_meta = load("fault-meta.json")
fault_badshape = load("fault-d1-badshape.json")


def rate(x):
    v = x.get("iters_per_sec")
    return v if isinstance(v, (int, float)) else None


def ratio(solo, corun):
    rs, rc = rate(solo), rate(corun)
    if rs and rc is not None and rs > 0:
        return round(rc / rs, 4)
    return None


compute_ratio = ratio(compute_solo, compute_d0)
bandwidth_ratio = ratio(bandwidth_solo, bandwidth_d0)

# 용량: 같은 크기(CAP_K/CAP_DIM)가 solo 에서 성공한 것과 같이 co-run 에서도 성공하는가만
# 본다(ponytail: 최대 할당량을 찾는 이분 탐색은 하지 않음 — rbln-smi 메모리 스냅샷은
# 증거 문서에 별도로 싣는 진단 자료).
if capacity_solo.get("error") or capacity_d0.get("error") or rate(capacity_d0) is None:
    capacity = "shrinks"
else:
    capacity = "same"

# 형상 불일치 주입 자체가 실제로 실패했는지(error 필드 존재)를 요구한다 — 우연히 호환되는
# 형상으로 돌려 조용히 성공하면 장애가 걸린 적이 없으므로 "contained" 를 찍을 근거가 없다.
if (
    not fault_d0.get("error")
    and fault_meta.get("d1_killed_confirmed") is True
    and fault_meta.get("post_smi_ok") is True
    and bool(fault_badshape.get("error"))
):
    fault = "contained"
else:
    fault = "leaks"


def fmt(v):
    return "unmeasured" if v is None else v


line = "ISOLATION: compute=%s capacity=%s bandwidth=%s fault=%s" % (
    fmt(compute_ratio),
    capacity,
    fmt(bandwidth_ratio),
    fault,
)
verdict = {
    "computeRatio": compute_ratio,
    "capacity": capacity,
    "bandwidthRatio": bandwidth_ratio,
    "fault": fault,
    "computeSolo": compute_solo,
    "computeD0": compute_d0,
    "capacitySolo": capacity_solo,
    "capacityD0": capacity_d0,
    "bandwidthSolo": bandwidth_solo,
    "bandwidthD0": bandwidth_d0,
    "faultD0": fault_d0,
    "faultMeta": fault_meta,
    "faultBadshape": fault_badshape,
    "line": line,
}
with open(os.path.join(d, "verdict.json"), "w") as f:
    json.dump(verdict, f, ensure_ascii=False, indent=2)
print(line)
PY
}

# ── PHASE=verdict 전용 경로: 라이브 접근 없이 RUN_DIR 의 저장 파일만으로 한 줄 출력 ──────────
if [ "${PHASE:-}" = "verdict" ]; then
  isolation_verdict_line "$RUN_DIR"
  exit 0
fi

# ── preflight ────────────────────────────────────────────────────────────────────────────
preflight() {
  PHASE_STATE="preflight"
  log "### preflight (node=$NODE ssh=$NODE_SSH dev0=$DEV0 dev1=$DEV1 run_id=$RUN_ID)"
  if [ "$DRY_RUN" = "1" ]; then log "  [DRY_RUN] preflight 생략"; return 0; fi

  local ready
  ready="$(kubeget get node "$NODE" -o jsonpath='{range .status.conditions[?(@.type=="Ready")]}{.status}{end}' 2>/dev/null || echo "")"
  record "node_ready" "$([ "$ready" = "True" ] && echo true || echo false)" "$NODE Ready=$ready"
  [ "$ready" = "True" ] || { set_blocked "ENVIRONMENT" "$NODE 가 Ready 아님(=$ready)"; return 1; }

  if ! kubeget get nodedevicereport "$NODE" >/dev/null 2>&1; then
    set_blocked "ENVIRONMENT" "NodeDeviceReport $NODE 없음"; return 1
  fi
  local n_dev
  n_dev="$(kubeget get nodedevicereport "$NODE" -o jsonpath='{range .status.devices[?(@.vendor=="rebellions")]}{.model}{" "}{end}' 2>/dev/null | wc -w | tr -d ' ')"
  record "ndr_two_rebellions_devices" "$([ "${n_dev:-0}" -ge 2 ] && echo true || echo false)" "장치 수=${n_dev:-0}"
  [ "${n_dev:-0}" -ge 2 ] || { set_blocked "ENVIRONMENT" "NDR $NODE 에 rebellions 장치가 2개 미만(=${n_dev:-0})"; return 1; }

  local d
  for d in "$DEV0" "$DEV1"; do
    if ! node_ssh "test -e /dev/rbln${d}" 2>/dev/null; then
      set_blocked "ENVIRONMENT" "$NODE 에 /dev/rbln${d} 없음(ssh)"; return 1
    fi
  done

  local svc
  # is-active 는 inactive/failed 에서도 stdout 에 상태를 찍고 exit 은 0 이 아니다 — `||` 로
  # 묶으면 두 줄(예: "inactive\nunknown")이 같이 잡힌다. exit 코드가 아니라 빈 문자열 여부로
  # 폴백을 가른다.
  svc="$(node_ssh "systemctl is-active $SERVICE" 2>/dev/null)"
  [ -n "$svc" ] || svc="unknown"
  SVC_BEFORE="$svc"
  record "service_present" "$([ "$svc" = "active" ] || [ "$svc" = "inactive" ] && echo true || echo false)" "$SERVICE(측정 전)=$svc"

  # 안전 게이트(읽기 전용): 이 노드에서 rebellions/atom 을 요구하는 파드가 있으면 중단.
  local claimants
  claimants="$(kubeget get pods -A -o json 2>/dev/null | jq --arg node "$NODE" '
      [.items[] | select(.spec.nodeName==$node) | .spec.containers[]
       | select((.resources.requests // {}) | keys | any(test("rebellions")))] | length
    ' 2>/dev/null || echo "?")"
  record "no_pod_claims_rebellions_atom" "$([ "$claimants" = "0" ] && echo true || echo false)" "요구 파드 수=$claimants"
  [ "$claimants" = "0" ] || { set_blocked "ENVIRONMENT" "$NODE 에서 rebellions/atom 을 요구하는 파드 ${claimants}개 존재"; return 1; }

  # 디스크 여유 확인(안전 조건) — <rebellions-node> 루트 가용량.
  local avail_line avail_h avail_num
  avail_line="$(node_ssh "df -h / 2>/dev/null | tail -1" 2>/dev/null || echo "")"
  log "  df -h /: $avail_line"
  avail_h="$(printf '%s' "$avail_line" | awk '{print $4}')"
  avail_num="${avail_h%G}"
  avail_num="${avail_num%.*}"
  if [ -n "$avail_num" ] && [ "$avail_num" -ge "$MIN_FREE_GIB" ] 2>/dev/null; then
    record "disk_headroom" true "가용=$avail_h(node5 /)"
  else
    record "disk_headroom" false "가용=$avail_h(최소 ${MIN_FREE_GIB}G 요구 또는 단위 확인 불가)"
    set_blocked "ENVIRONMENT" "node5 디스크 여유 부족 또는 확인 불가(가용=$avail_h)"
    return 1
  fi
}

# ── measure ──────────────────────────────────────────────────────────────────────────────
measure() {
  PHASE_STATE="measure"
  log "### measure: $NODE 에서 연산·용량·대역폭·장애 네 항목 실측(device0=$DEV0 device1=$DEV1)"
  if [ "$DRY_RUN" = "1" ]; then
    log "+ scp $WORKLOAD_FILE $MEASURE_FILE $NODE_SSH:/tmp/"
    log "+ ssh $NODE_SSH 'SERVICE=$SERVICE OUT=$REMOTE_OUT ... bash /tmp/measure-node-isolation.sh'"
    local f
    for f in compute-solo compute-d0 compute-d1 bandwidth-solo bandwidth-d0 bandwidth-d1 fault-d0; do
      printf '{"phase":"%s","iters":100,"seconds":20,"iters_per_sec":5.0,"device_open_seconds":0.01,"init_seconds":3.0}' "$f" >"$RUN_DIR/$f.json"
    done
    for f in capacity-solo capacity-d0 capacity-d1; do
      printf '{"phase":"%s","iters":10,"seconds":15,"iters_per_sec":0.7,"device_open_seconds":0.02,"init_seconds":4.0}' "$f" >"$RUN_DIR/$f.json"
    done
    printf '{}' >"$RUN_DIR/fault-d1-badshape.json"
    printf '{"d1_killed_rc":"137","d1_killed_confirmed":true,"post_smi_ok":true}' >"$RUN_DIR/fault-meta.json"
    RUN_DONE=true
    return 0
  fi

  scp -o BatchMode=yes -o ConnectTimeout=15 "$WORKLOAD_FILE" "$NODE_SSH:/tmp/rbln-workload-isolation.py" >&2
  scp -o BatchMode=yes -o ConnectTimeout=15 "$MEASURE_FILE" "$NODE_SSH:/tmp/measure-node-isolation.sh" >&2

  local out="$RUN_DIR/measure.out"
  if ! timeout "$T_MEASURE" ssh -o BatchMode=yes -o ConnectTimeout=15 "$NODE_SSH" \
      "SERVICE=$SERVICE OUT=$REMOTE_OUT DEV0=$DEV0 DEV1=$DEV1 DUR=$DUR BARRIER=$BARRIER \
       CAP_DUR=$CAP_DUR CAP_BARRIER=$CAP_BARRIER CAP_K=$CAP_K CAP_DIM=$CAP_DIM BW_DIM=$BW_DIM \
       MATMUL_DIM=$MATMUL_DIM FAULT_DUR=$FAULT_DUR FREE_WAIT=$FREE_WAIT \
       WL=/tmp/rbln-workload-isolation.py bash /tmp/measure-node-isolation.sh" \
      >"$out" 2>&1; then
    log "  경고: 원격 측정이 비정상 종료(로그 $out) — 파싱 계속"
  fi
  sed 's/\x1b\[[0-9;]*m//g' "$out" | tail -120 >&2 || true

  if grep -q '^MEASURE_BLOCKED' "$out"; then
    set_blocked "ENVIRONMENT" "node 측정 차단: $(grep '^MEASURE_BLOCKED' "$out" | head -1)"
    return 1
  fi

  extract() { grep "^${1} " "$out" | tail -1 | sed "s/^${1} //" >"$RUN_DIR/${2}.json"; }
  extract COMPUTE_SOLO_RESULT compute-solo
  extract COMPUTE_D0_RESULT compute-d0
  extract COMPUTE_D1_RESULT compute-d1
  extract CAPACITY_SOLO_RESULT capacity-solo
  extract CAPACITY_D0_RESULT capacity-d0
  extract CAPACITY_D1_RESULT capacity-d1
  extract BANDWIDTH_SOLO_RESULT bandwidth-solo
  extract BANDWIDTH_D0_RESULT bandwidth-d0
  extract BANDWIDTH_D1_RESULT bandwidth-d1
  extract FAULT_D0_RESULT fault-d0
  extract FAULT_D1_BADSHAPE_RESULT fault-d1-badshape

  awk '/^CAPACITY_SOLO_SMI_BEGIN/{f=1;next}/^CAPACITY_SOLO_SMI_END/{f=0}f' "$out" >"$RUN_DIR/capacity-solo-smi.txt"
  awk '/^CAPACITY_CORUN_SMI_BEGIN/{f=1;next}/^CAPACITY_CORUN_SMI_END/{f=0}f' "$out" >"$RUN_DIR/capacity-corun-smi.txt"
  awk '/^FAULT_POST_SMI_BEGIN/{f=1;next}/^FAULT_POST_SMI_END/{f=0}f' "$out" >"$RUN_DIR/fault-post-smi.txt"

  local d1_killed_rc d1_killed_confirmed post_smi_rc post_smi_ok
  d1_killed_rc="$(grep -oE '^MEASURE_RC fault_d1_killed=-?[0-9]+' "$out" | tail -1 | sed 's/.*=//')"
  # SIGKILL 로 죽으면 종료코드는 보통 137(128+9), 셸에 따라 -9 로도 나타난다.
  if [ "${d1_killed_rc:-}" = "137" ] || [ "${d1_killed_rc:-}" = "-9" ]; then
    d1_killed_confirmed=true
  else
    d1_killed_confirmed=false
  fi
  post_smi_rc="$(grep -oE '^MEASURE_FAULT_POST_SMI_RC [0-9]+' "$out" | tail -1 | awk '{print $2}')"
  [ "${post_smi_rc:-1}" = "0" ] && post_smi_ok=true || post_smi_ok=false
  printf '{"d1_killed_rc":"%s","d1_killed_confirmed":%s,"post_smi_ok":%s}' \
    "${d1_killed_rc:-unknown}" "$d1_killed_confirmed" "$post_smi_ok" >"$RUN_DIR/fault-meta.json"
  record "fault_d1_killed_confirmed" "$d1_killed_confirmed" "rc=${d1_killed_rc:-unknown}"
  record "fault_post_smi_ok" "$post_smi_ok" "rc=${post_smi_rc:-unknown}"

  SVC_FINAL="$(grep -oE '^SERVICE_STATE [a-z]+' "$out" | tail -1 | awk '{print $2}')"

  local ok=true f
  for f in compute-solo compute-d0 compute-d1 bandwidth-solo bandwidth-d0 bandwidth-d1 fault-d0; do
    if python3 -c "import json,sys;d=json.load(open(sys.argv[1]));sys.exit(0 if d.get('iters_per_sec') is not None else 1)" "$RUN_DIR/$f.json" 2>/dev/null; then
      record "${f}_completed" true ""
    else
      record "${f}_completed" false "$(cat "$RUN_DIR/$f.json" 2>/dev/null || echo '(파일 없음)')"
      ok=false
    fi
  done
  [ "$ok" = true ] || { set_blocked "ENVIRONMENT" "필수 항목 중 하나 이상 RESULT 미완"; return 1; }
  RUN_DONE=true
}

# ── verdict ──────────────────────────────────────────────────────────────────────────────
compute_verdict() {
  PHASE_STATE="verdict"
  ISOLATION_LINE="$(isolation_verdict_line "$RUN_DIR")"
  printf '%s\n' "$ISOLATION_LINE" | tee "$RUN_DIR/isolation-line.txt" >&2
  case "$ISOLATION_LINE" in
    *unmeasured*) record "isolation_line_complete" false "$ISOLATION_LINE" ;;
    *) record "isolation_line_complete" true "$ISOLATION_LINE" ;;
  esac
}

# ── cleanup ──────────────────────────────────────────────────────────────────────────────
cleanup() {
  [ "$CLEANED" = true ] && return 0
  CLEANED=true
  PHASE_STATE="cleanup"
  log "### cleanup: $SERVICE 복원 확인(원격 스크립트가 이미 복원+삭제했을 것 — 이중 확인)"
  if [ "$DRY_RUN" != "1" ]; then
    local svc
    # is-active 는 inactive/failed 에서도 stdout 에 상태를 찍고 exit 은 0 이 아니다 — 빈
    # 문자열 여부로 폴백을 가른다(run.sh 다른 자리와 동일 계약).
    svc="$(node_ssh "systemctl is-active $SERVICE" 2>/dev/null)"
    [ -n "$svc" ] || svc="unknown"
    if [ "$svc" != "active" ]; then
      log "  $SERVICE=$svc — 복원 시도"
      node_ssh "sudo systemctl start $SERVICE" 2>/dev/null || log "  경고: $SERVICE 복원 실패 — 수동 확인 필요"
      sleep 3
      svc="$(node_ssh "systemctl is-active $SERVICE" 2>/dev/null)"
      [ -n "$svc" ] || svc="unknown"
    fi
    SVC_FINAL="$svc"
    record "service_restored" "$([ "$svc" = "active" ] && echo true || echo false)" "$SERVICE(측정 전=$SVC_BEFORE)=$svc"
    local remote_left
    remote_left="$(node_ssh "test -d $REMOTE_OUT && echo present || echo absent" 2>/dev/null || echo unknown)"
    record "remote_outdir_removed" "$([ "$remote_left" = "absent" ] && echo true || echo false)" "$REMOTE_OUT=$remote_left"
  fi
}

finish() {
  if [ "$CLEANED" != true ]; then cleanup || true; fi
  local verdict="fail" dry="false"
  if [ "$DRY_RUN" = "1" ]; then
    verdict="dry"; dry="true"
  elif [ "$BLOCKED" = true ]; then
    verdict="blocked"
  elif [ "$RUN_DONE" = true ] && [ "$FAILS" -eq 0 ]; then
    verdict="pass"
  fi
  printf 'verdict=%s\n' "$verdict"
  printf '{"test":"rebellions-isolation","vendor":"%s","node":"%s","dev0":"%s","dev1":"%s","phase":"%s","classification":"%s","blockReason":"%s","fails":%s,"verdict":"%s","dryRun":%s,"isolationLine":"%s","serviceBefore":"%s","serviceFinal":"%s","runId":"%s","date":"%s"}\n' \
    "$VENDOR" "$NODE" "$DEV0" "$DEV1" "$PHASE_STATE" "$CLASSIFICATION" "$BLOCK_REASON" "$FAILS" "$verdict" "$dry" "${ISOLATION_LINE//\"/\\\"}" "${SVC_BEFORE:-unknown}" "${SVC_FINAL:-unknown}" "$RUN_ID" "$(utc_now)" \
    | tee -a "$RUN_DIR/summary-$(date +%H%M%S).json"
  [ "$verdict" = "pass" ] || [ "$verdict" = "dry" ] || exit 1
  exit 0
}

trap finish EXIT
# 신호로 끊겨도 EXIT 트랩(정리)이 돌도록 신호를 종료 코드로 바꾼다(128+신호 번호) —
# 트랩이 없는 SIGTERM·SIGHUP 은 셸을 바로 끝내 EXIT 트랩이 돌지 않는다.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

preflight || true
if [ "$BLOCKED" != true ]; then measure || true; fi
if [ "$BLOCKED" != true ] && [ "$RUN_DONE" = true ]; then compute_verdict; fi
# EXIT trap(finish) 이 cleanup + verdict 라인 + JSON 요약 + 종료코드를 담당한다.
