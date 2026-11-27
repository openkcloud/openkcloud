#!/usr/bin/env bash
# ============================================================
# run.sh: Rebellions ATOM SW 시분할 실 워크로드 동시 실행 라이브 하네스(<rebellions-node>)
# 상세: descriptor 적용→실측(host 원격 프로세스 또는 pods)→판정→정리 순서로 돈다(모드·판정
#       기준은 README 참고). 자체 descriptor 와 <rebellions-node> 의 atomplus-vllm.service 만 건드린다.
# 생성일: 2026-09-25 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DRY_RUN="${DRY_RUN:-0}"
KCTX="${KUBECTL_CONTEXT:-kubernetes-admin@cluster.local}"
REQUESTED_PHASE="${PHASE:-all}"   # PHASE=verdict 오프라인 재판정 요청 캡처(내부 PHASE 상태값과 별도 보관)

VENDOR="${VENDOR:-rebellions}"
NODE="${NODE:-node5}"
# NODE_SSH 기본값은 kubectl InternalIP 로 해석한다(고정 주소 대신 — 노드 대상은 kubectl 로만
# 식별한다). DRY_RUN 이면 클러스터를 읽지 않고 표시용 placeholder 를 쓴다.
if [ -n "${NODE_SSH:-}" ]; then
  :  # 환경변수로 이미 지정됨 — 유지
elif [ "${DRY_RUN:-0}" = "1" ] || [ "${REQUESTED_PHASE:-all}" = "verdict" ]; then
  NODE_SSH="dry-run-node-ip"   # PHASE=verdict 오프라인 재판정은 노드를 접촉하지 않는다
else
  NODE_SSH="$(kubectl --context "$KCTX" get node "$NODE" -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null)" || true
fi
SERVICE="${SERVICE:-atomplus-vllm.service}"

DESC_FILE="${DESC_FILE:-$SCRIPT_DIR/descriptor.yaml}"
WORKLOAD_FILE="${WORKLOAD_FILE:-$SCRIPT_DIR/workload.py}"
MEASURE_FILE="${MEASURE_FILE:-$SCRIPT_DIR/measure-node.sh}"

DURATION="${DURATION:-30}"
BARRIER_SECONDS="${BARRIER_SECONDS:-40}"
MATMUL_DIM="${MATMUL_DIM:-2048}"
DEVICE_ID="${DEVICE_ID:-0}"

RESOURCE_DESC="${RESOURCE_DESC:-rebellions/atom}"        # descriptor 광고(vendor/product)
RESOURCE_VENDOR="${RESOURCE_VENDOR:-rebellions.ai/ATOM}" # NPUClusterPolicy 벤더 광고
REPLICAS="${REPLICAS:-2}"

NAMESPACE="${NAMESPACE:-kcloud}"
NCP="${NCP:-npuclusterpolicy-sample}"
NCP_DS_NS="${NCP_DS_NS:-kube-system}"
NCP_DS="${NCP_DS:-rbln-device-plugin}"

MEASURE_MODE="${MEASURE_MODE:-host}"          # host(호스트 프로세스) | pods(CR 광고 자원으로 파드 스케줄)
# 파드 이미지 태그는 pod-*.yaml 에 고정값으로 두고, env 다섯(MATMUL_DIM·DURATION·DEVICE_ID·
# RBLN_NPU_NAME·RBLN_COMPILED_PATH)은 render_pod 가 여기 변수로 채운다.
CONSUMER_NS="${CONSUMER_NS:-default}"
POD_WAIT_SECONDS="${POD_WAIT_SECONDS:-420}"
CM_NAME="rbln-ts-workload"
POD_SOLO="rbln-ts-solo"; POD_A="rbln-ts-corun-a"; POD_B="rbln-ts-corun-b"
RBLN_NPU_NAME="${RBLN_NPU_NAME:-RBLN-CA22}"   # pod-solo/pod-a/pod-b 의 RBLN_NPU_NAME 폴백과 동일값
CACHE_DIR="${CACHE_DIR:-/home/kcloud/rbln-ts-cache}"           # 호스트 사전 컴파일 캐시
CACHE_FILE="matmul-${MATMUL_DIM}-${RBLN_NPU_NAME}.rbln"
CACHE_PATH="$CACHE_DIR/$CACHE_FILE"                            # render_pod 가 파드 env RBLN_COMPILED_PATH(/models/$CACHE_FILE)로 채운다

name_from() { sed -nE 's/^[[:space:]]*name:[[:space:]]*([^[:space:]]+).*/\1/p' "$1" | head -1; }
DESC_NAME="${DESC_NAME:-$(name_from "$DESC_FILE")}"
GEN_DS="kcloud-generated-${DESC_NAME}-dp"
GEN_NS="${GEN_NS:-default}"

T_DS_DELETE="${T_DS_DELETE:-180}"
T_RESTORE="${T_RESTORE:-240}"

T_GEN_UP="${T_GEN_UP:-180}"
T_GC="${T_GC:-180}"
IV="${IV:-5}"
# node 실측 총 시간 상한: 서비스 정지 대기 + solo + 배리어 + co-run + 여유.
T_MEASURE="${T_MEASURE:-$((BARRIER_SECONDS + 2 * DURATION + 240))}"

OUT="${OUT:-/tmp/rebellions-timeslice-evidence}"
mkdir -p "$OUT"

RESOURCE_DESC_JP="${RESOURCE_DESC//./\\.}"
RESOURCE_VENDOR_JP="${RESOURCE_VENDOR//./\\.}"

ORIG_ENABLED=""
NCP_PATCHED=false
DESC_APPLIED=false
RUN_DONE=false
BLOCKED=false
CLASSIFICATION=""
BLOCK_REASON=""
CLEANED=false
CHECK_JSON=""
FAILS=0
PHASE="init"
VERDICT_JSON=""
INIT_A=""
INIT_B=""
OPEN_A=""
OPEN_B=""
SMI_PIDS=""
SVC_FINAL=""
ORIG_SERVICE_STATE=""

utc_now() { date -u +%FT%TZ; }
log() { printf '%s\n' "$*" >&2; }

kc() {
  if [ "$DRY_RUN" = "1" ]; then printf '+ kubectl --context %s %s\n' "$KCTX" "$*" >&2; return 0; fi
  kubectl --context "$KCTX" "$@"
}
kubeget() { if [ "$DRY_RUN" = "1" ]; then return 0; fi; kubectl --context "$KCTX" "$@"; }

node_ssh() { ssh -o BatchMode=yes -o ConnectTimeout=15 "$NODE_SSH" "$@"; }

record() {
  local name="$1" ok="$2" detail="${3:-}"
  if [ "$ok" != "true" ]; then FAILS=$((FAILS + 1)); fi
  CHECK_JSON="${CHECK_JSON:+$CHECK_JSON,}\"$name\":$ok"
  local tag; tag=$([ "$ok" = "true" ] && echo PASS || echo FAIL)
  log "  [$tag] $name ${detail:+— $detail}"
}

set_blocked() {
  BLOCKED=true
  CLASSIFICATION="$1"
  BLOCK_REASON="$2"
  log "BLOCKED[$1]: $2"
}

alloc_desc() { kubeget get node "$NODE" -o jsonpath="{.status.allocatable.$RESOURCE_DESC_JP}" 2>/dev/null || echo ""; }
alloc_vendor() { kubeget get node "$NODE" -o jsonpath="{.status.allocatable.$RESOURCE_VENDOR_JP}" 2>/dev/null || echo ""; }
gen_ds_present() { kubeget -n "$GEN_NS" get ds "$GEN_DS" >/dev/null 2>&1; }
gen_ds_absent()  { ! gen_ds_present; }
gen_advertised_n() { local a; a="$(alloc_desc)"; [ -n "$a" ] && [ "$a" != "0" ] && [ "${a:-0}" -ge "$REPLICAS" ] 2>/dev/null; }
ncp_patch() { kc -n "$NAMESPACE" patch npuclusterpolicy "$NCP" --type=merge -p "$1" >&2; }
ncp_ds_present() { kubeget -n "$NCP_DS_NS" get ds "$NCP_DS" >/dev/null 2>&1; }
ncp_ds_absent()  { ! ncp_ds_present; }
ncp_ds_up() { local v; v="$(alloc_vendor)"; [ -n "$v" ] && [ "$v" != "0" ]; }
# ncp_vendor_receded — RESOURCE_VENDOR allocatable 이 0 이거나 키가 없으면 참. 키가 사라지길
# 기다리면 값이 0 에서 멈춰 매번 타임아웃 예산을 다 쓴다.
ncp_vendor_receded() { local v; v="$(alloc_vendor)"; [ -z "$v" ] || [ "$v" = "0" ]; }

poll() {
  local t="$1" iv="$2"; shift 2
  if [ "$DRY_RUN" = "1" ]; then printf '+ (poll <=%ss) %s\n' "$t" "$*" >&2; return 0; fi
  local e=0
  while [ "$e" -lt "$t" ]; do
    if "$@"; then return 0; fi
    sleep "$iv"; e=$((e + iv))
  done
  return 1
}

# ── preflight ──────────────────────────────────────────────────────────────
preflight() {
  PHASE="preflight"
  log "### preflight (ctx=$KCTX vendor=$VENDOR node=$NODE ssh=$NODE_SSH desc=$DESC_NAME resource=$RESOURCE_DESC replicas=$REPLICAS duration=$DURATION barrier=$BARRIER_SECONDS device_id=$DEVICE_ID)"
  if [ "$DRY_RUN" = "1" ]; then log "  [DRY_RUN] preflight 생략"; return 0; fi

  local ready; ready="$(kubeget get node "$NODE" -o jsonpath='{range .status.conditions[?(@.type=="Ready")]}{.status}{end}' 2>/dev/null || echo "")"
  record "node_ready" "$([ "$ready" = "True" ] && echo true || echo false)" "$NODE Ready=$ready"
  [ "$ready" = "True" ] || { set_blocked "ENVIRONMENT" "$NODE 가 Ready 아님(=$ready)"; return 1; }

  if ! kubeget get nodedevicereport "$NODE" >/dev/null 2>&1; then
    set_blocked "ENVIRONMENT" "NodeDeviceReport $NODE 없음"; return 1
  fi
  local has_rbln; has_rbln="$(kubeget get nodedevicereport "$NODE" -o jsonpath='{range .status.devices[?(@.vendor=="rebellions")]}{.model}{" "}{end}' 2>/dev/null || echo "")"
  record "ndr_rebellions_present" "$([ -n "$has_rbln" ] && echo true || echo false)" "models=[$has_rbln]"
  [ -n "$has_rbln" ] || { set_blocked "ENVIRONMENT" "NDR $NODE 에 rebellions 장치 없음"; return 1; }

  if ! node_ssh "test -e /dev/rbln${DEVICE_ID}" 2>/dev/null; then
    set_blocked "ENVIRONMENT" "$NODE 에 /dev/rbln${DEVICE_ID} 없음(ssh)"; return 1
  fi
  local svc; svc="$(node_ssh "systemctl is-active $SERVICE" 2>/dev/null || echo unknown)"
  ORIG_SERVICE_STATE="$svc"   # cleanup 이 이 시작값으로만 복원한다 — host·pods 모드 공통
  record "service_present" "$([ "$svc" = "active" ] || [ "$svc" = "inactive" ] && echo true || echo false)" "$SERVICE=$svc"
}

# ── advertise: descriptor 공유 광고(운영자 sharing 게이트가 rebellions 를 받는지) ──────
# 벤더가 NPUClusterPolicy 로 관리 중이면 auto-apply 가 이중 광고 방지로 거절하므로(tt 선례와
# 동일), 벤더를 잠시 끄고 descriptor 광고를 확인한 뒤 되돌린다. 실측(measure)의 전제 조건이
# 아니므로 여기서 실패해도 set_blocked 하지 않는다 — 호스트 실측은 그대로 진행한다.
advertise_check() {
  PHASE="advertise"
  if [ "$DRY_RUN" != "1" ]; then
    ORIG_ENABLED="$(kubeget get npuclusterpolicy "$NCP" -n "$NAMESPACE" -o jsonpath="{.spec.$VENDOR.enabled}" 2>/dev/null || echo "")"
  fi
  log "### advertise: $VENDOR 를 잠시 끄고 descriptor 로 $RESOURCE_DESC 를 $REPLICAS 개 광고 확인 (원값 enabled=$ORIG_ENABLED)"

  log "  1) $VENDOR.enabled=false → $NCP_DS_NS/$NCP_DS 회수"
  ncp_patch "{\"spec\":{\"$VENDOR\":{\"enabled\":false}}}"
  NCP_PATCHED=true
  poll "$T_DS_DELETE" "$IV" ncp_ds_absent || log "  경고: $NCP_DS_NS/$NCP_DS ${T_DS_DELETE}초 내 미삭제"

  log "  2) descriptor 적용 → 생성 device-plugin 광고 대기"
  kc apply -f "$DESC_FILE" >&2
  DESC_APPLIED=true
  if poll "$T_GEN_UP" "$IV" gen_advertised_n; then
    record "descriptor_advertised_n" true "$RESOURCE_DESC=$(alloc_desc) (sharingRefusal 이 rebellions timeSlicing=Software 를 통과시킴)"
  else
    record "descriptor_advertised_n" false "${T_GEN_UP}초 내 $REPLICAS 미광고(현재=$(alloc_desc))"
    kubectl --context "$KCTX" get acceleratordescriptor "$DESC_NAME" -o jsonpath='{"feasible="}{.status.feasibleBackends}{" refusals="}{range .status.refusals[*]}{.backend}{": "}{.reason}{" | "}{end}{"\n"}' 2>&1 | tee "$OUT/descriptor-status.txt" >&2 || true
  fi

  log "  3) descriptor 제거 → $VENDOR 재활성 복원"
  kc delete acceleratordescriptor "$DESC_NAME" --ignore-not-found >&2 2>/dev/null || true
  DESC_APPLIED=false
  poll "$T_GC" "$IV" gen_ds_absent || log "  경고: $GEN_NS/$GEN_DS ${T_GC}초 내 미회수"
  restore_vendor
}

# restore_vendor 는 NPUClusterPolicy 의 벤더를 원래대로 켜고 벤더 device-plugin 광고 복원을 본다.
restore_vendor() {
  [ "$NCP_PATCHED" = true ] || return 0
  ncp_patch "{\"spec\":{\"$VENDOR\":{\"enabled\":${ORIG_ENABLED:-true}}}}" 2>/dev/null || \
    log "  경고: $VENDOR 재활성 패치 실패 — 수동 확인 필요"
  NCP_PATCHED=false
  if poll "$T_RESTORE" "$IV" ncp_ds_up; then
    record "vendor_dp_restored" true "$RESOURCE_VENDOR=$(alloc_vendor)"
  else
    record "vendor_dp_restored" false "${T_RESTORE}초 내 $RESOURCE_VENDOR 미복원(현재=$(alloc_vendor))"
  fi
}

# ── measure: <rebellions-node> 에서 두 프로세스를 같은 장치에 고정해 실측 ─────────────────
measure() {
  if [ "$MEASURE_MODE" = "pods" ]; then measure_pods; return; fi
  PHASE="measure"
  log "### measure: $NODE 에서 solo → co-run(같은 DEVICE_ID=$DEVICE_ID) 실측"
  if [ "$DRY_RUN" = "1" ]; then
    log "+ scp $WORKLOAD_FILE $MEASURE_FILE $NODE_SSH:/tmp/"
    log "+ ssh $NODE_SSH 'DUR=$DURATION DIM=$MATMUL_DIM BAR=$BARRIER_SECONDS DEVICE_ID=$DEVICE_ID SERVICE=$SERVICE bash /tmp/measure-node.sh'"
    printf '{"phase":"solo","iters":900,"seconds":%s,"iters_per_sec":30.0,"device_open_seconds":0.01,"init_seconds":3.4,"start":"2026-01-01T00:00:00Z","end":"2026-01-01T00:00:30Z"}' "$DURATION" > "$OUT/solo.json"
    printf '{"phase":"corun-a","iters":760,"seconds":%s,"iters_per_sec":25.0,"device_open_seconds":0.01,"init_seconds":3.4,"start":"2026-01-01T00:01:00Z","end":"2026-01-01T00:01:30Z"}' "$DURATION" > "$OUT/corun-a.json"
    printf '{"phase":"corun-b","iters":760,"seconds":%s,"iters_per_sec":25.0,"device_open_seconds":0.01,"init_seconds":3.4,"start":"2026-01-01T00:01:00Z","end":"2026-01-01T00:01:30Z"}' "$DURATION" > "$OUT/corun-b.json"
    SMI_PIDS=2; RUN_DONE=true
    return 0
  fi

  scp -o BatchMode=yes -o ConnectTimeout=15 "$WORKLOAD_FILE" "$NODE_SSH:/tmp/rbln-workload.py" >&2
  scp -o BatchMode=yes -o ConnectTimeout=15 "$MEASURE_FILE" "$NODE_SSH:/tmp/measure-node.sh" >&2

  local out="$OUT/measure.out"
  if ! timeout "$T_MEASURE" ssh -o BatchMode=yes -o ConnectTimeout=15 "$NODE_SSH" \
      "DUR=$DURATION DIM=$MATMUL_DIM BAR=$BARRIER_SECONDS DEVICE_ID=$DEVICE_ID SERVICE=$SERVICE WL=/tmp/rbln-workload.py bash /tmp/measure-node.sh" \
      > "$out" 2>&1; then
    log "  경고: 원격 측정이 비정상 종료(로그 $out) — 파싱 계속"
  fi
  sed 's/\x1b\[[0-9;]*m//g' "$out" | tail -40 >&2 || true

  if grep -q '^MEASURE_BLOCKED' "$out"; then
    set_blocked "ENVIRONMENT" "node 측정 차단: $(grep '^MEASURE_BLOCKED' "$out" | head -1)"
    return 1
  fi

  # SOLO/CORUN RESULT json 을 파일로 분리.
  grep '^SOLO_RESULT ' "$out" | tail -1 | sed 's/^SOLO_RESULT //' > "$OUT/solo.json"
  grep '^CORUN_A_RESULT ' "$out" | tail -1 | sed 's/^CORUN_A_RESULT //' > "$OUT/corun-a.json"
  grep '^CORUN_B_RESULT ' "$out" | tail -1 | sed 's/^CORUN_B_RESULT //' > "$OUT/corun-b.json"
  SMI_PIDS="$(grep -oE "^CORUN_SMI_NPU${DEVICE_ID}_DISTINCT_PIDS [0-9]+" "$out" | tail -1 | awk '{print $2}')"
  SVC_FINAL="$(grep -oE '^SERVICE_STATE [a-z]+' "$out" | tail -1 | awk '{print $2}')"

  # 초기화 시간을 요약에 싣는다.
  INIT_A="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("init_seconds",""))' "$OUT/corun-a.json" 2>/dev/null || echo "")"
  INIT_B="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("init_seconds",""))' "$OUT/corun-b.json" 2>/dev/null || echo "")"
  OPEN_A="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("device_open_seconds",""))' "$OUT/corun-a.json" 2>/dev/null || echo "")"
  OPEN_B="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("device_open_seconds",""))' "$OUT/corun-b.json" 2>/dev/null || echo "")"
  log "  co-run init_seconds: a=${INIT_A:-?} b=${INIT_B:-?} | device_open_seconds: a=${OPEN_A:-?} b=${OPEN_B:-?} | smi NPU${DEVICE_ID} distinct PIDs=${SMI_PIDS:-?}"

  local a; a="$(python3 -c 'import json,sys;print("ok" if json.load(open(sys.argv[1])).get("iters_per_sec") is not None else "no")' "$OUT/corun-a.json" 2>/dev/null || echo no)"
  local b; b="$(python3 -c 'import json,sys;print("ok" if json.load(open(sys.argv[1])).get("iters_per_sec") is not None else "no")' "$OUT/corun-b.json" 2>/dev/null || echo no)"
  record "corun_a_completed" "$([ "$a" = ok ] && echo true || echo false)" "$(cat "$OUT/corun-a.json")"
  record "corun_b_completed" "$([ "$b" = ok ] && echo true || echo false)" "$(cat "$OUT/corun-b.json")"
  [ "$a" = ok ] && [ "$b" = ok ] || { set_blocked "ENVIRONMENT" "co-run 파드 중 RESULT 미완"; return 1; }
  RUN_DONE=true
}

# result_json_from_pod <pod> <출력 파일> — 파드 로그의 "RESULT {json}" 마지막 줄만 json 파일로 남긴다.
result_json_from_pod() {
  local pod="$1" out="$2"
  kubeget -n "$CONSUMER_NS" logs "$pod" 2>/dev/null | sed -n 's/^RESULT //p' | tail -1 > "$out"
  [ -s "$out" ] || { log "  $pod: RESULT 줄 없음"; return 1; }
}

pod_done() { local p; p="$(kubeget -n "$CONSUMER_NS" get pod "$1" -o jsonpath='{.status.phase}' 2>/dev/null)"; [ "$p" = "Succeeded" ] || [ "$p" = "Failed" ]; }
both_running() { local a b; a="$(kubeget -n "$CONSUMER_NS" get pod "$POD_A" -o jsonpath='{.status.phase}' 2>/dev/null)"; b="$(kubeget -n "$CONSUMER_NS" get pod "$POD_B" -o jsonpath='{.status.phase}' 2>/dev/null)"; [ "$a" = "Running" ] && [ "$b" = "Running" ]; }

# render_pod <원본 yaml> [START_AT] — 파드 매니페스트의 env 값을 run.sh 변수로 채운 사본 경로를
# 낸다(단일 출처: MATMUL_DIM·DURATION·DEVICE_ID·RBLN_NPU_NAME·RBLN_COMPILED_PATH, 주면 START_AT).
# 매니페스트에 적힌 값은 사람이 kubectl 로 직접 적용할 때의 기본값이고, 하네스는 항상 이 렌더를
# 거친다. env 가 정확히 한 번 있어야 한다 — 없거나 두 번이면 실패해 조용한 불일치를 막는다.
render_pod() {
  python3 - "$1" "$MATMUL_DIM" "$DURATION" "$DEVICE_ID" "$RBLN_NPU_NAME" "/models/$CACHE_FILE" "${2:-}" <<'PY'
import sys, re
src, dim, dur, dev, npu, path, start = sys.argv[1:8]
s = open(src).read()
def put(name, val):
    global s
    s, n = re.subn(r'(- name: %s\n\s+value: )"?[^"\n]*"?' % re.escape(name), r'\1"%s"' % val, s)
    if n != 1:
        raise SystemExit("%s: env %s 가 %d 번 — 정확히 한 번이어야 한다" % (src, name, n))
for name, val in (("MATMUL_DIM", dim), ("DURATION", dur), ("DEVICE_ID", dev),
                  ("RBLN_NPU_NAME", npu), ("RBLN_COMPILED_PATH", path)):
    put(name, val)
if start:
    put("START_AT", start)
out = src + '.rendered.yaml'
open(out, 'w').write(s)
print(out)
PY
}

# rbln_live_pid_count — stdin 의 rbln-smi 출력에서 DEVICE_ID 행 중 status 가 killed 인 죽은
# 컨텍스트를 빼고 살아있는 서로 다른 PID 수를 센다(<rebellions-node> 사전 점검에서 killed 상태 컨텍스트
# 2개가 상시 남아 있음을 확인 — 게이트·co-run 스냅샷 둘 다 이 함수 하나로 판단해야 한다).
rbln_live_pid_count() {
  sed 's/\x1b\[[0-9;]*m//g' | grep -E "^\| +${DEVICE_ID} +\|" | grep -iv killed \
    | grep -oE '\| +[0-9]{3,} +\|' | grep -oE '[0-9]{3,}' | sort -u | wc -l | tr -d ' '
}

# precompile_host — <rebellions-node> 에 사전 컴파일 모델(CACHE_PATH)을 만든다(이미 있으면 재사용). 파드 안
# compile_from_torch 는 벤더가 실패 원인을 봉인해 둬 항상 실패하므로
# 컴파일은 호스트에서 한 번 하고 파드는 RBLN_COMPILED_PATH 로 적재만 한다.
# 이 함수는 measure_pods 의 DRY_RUN 조기 반환 뒤에서만 호출되므로 자체 DRY_RUN 분기가 없다.
precompile_host() {
  log "### precompile(host): $CACHE_PATH 준비"
  if node_ssh "test -e '$CACHE_PATH'" 2>/dev/null; then
    log "  재사용: $CACHE_PATH 이미 있음"
    return 0
  fi
  node_ssh "mkdir -p '$CACHE_DIR'" >&2
  scp -o BatchMode=yes -o ConnectTimeout=15 "$WORKLOAD_FILE" "$NODE_SSH:$CACHE_DIR/workload.py" >&2
  if ! node_ssh "RBLN_COMPILED_PATH='$CACHE_PATH' PHASE=compile-only MATMUL_DIM='$MATMUL_DIM' python3 '$CACHE_DIR/workload.py'" >&2; then
    set_blocked "ENVIRONMENT" "호스트 사전 컴파일 실패($CACHE_PATH)"
    return 1
  fi
  record "precompiled" true "$CACHE_PATH"
}

# ── measure_pods: descriptor 가 광고한 rebellions/atom 으로 파드 3개(solo, corun-a, corun-b)를
# <rebellions-node> 에 스케줄해 workload.py 를 돌린다. 장치 경합은 host 모드와 같이 /dev/rbln0+KMD 수준이다.
# 순서: 서비스 정지 → 게이트(외부 PID 0) → descriptor 재확인 → 호스트 사전 컴파일 → ConfigMap →
# solo → co-run(같은 START_AT) → rbln-smi 스냅샷 → RESULT 수집. 서비스 복원은 cleanup 이 시작값
# (ORIG_SERVICE_STATE) 기준으로 맡는다 — 다른 계정이 이 서비스를 운영하므로 우리가 멈춘 것만
# 다시 켠다.
measure_pods() {
  PHASE="measure"
  log "### measure(pods): $NODE 에서 파드 3개(solo, corun-a, corun-b)로 측정"
  if [ "$DRY_RUN" = "1" ]; then
    log "+ (pods) ssh $NODE_SSH systemctl stop $SERVICE / rbln-smi 게이트 / descriptor 재확인 / precompile_host / kubectl apply pod-solo,pod-a,pod-b"
    printf '{"phase":"solo","iters":900,"seconds":%s,"iters_per_sec":30.0,"device_open_seconds":0.01,"init_seconds":3.4,"start":"2026-01-01T00:00:00Z","end":"2026-01-01T00:00:30Z","compile_seconds":0,"compiled_from":"file"}' "$DURATION" > "$OUT/solo.json"
    printf '{"phase":"corun-a","iters":760,"seconds":%s,"iters_per_sec":25.0,"device_open_seconds":0.01,"init_seconds":3.4,"start":"2026-01-01T00:01:00Z","end":"2026-01-01T00:01:30Z","compile_seconds":0,"compiled_from":"file"}' "$DURATION" > "$OUT/corun-a.json"
    printf '{"phase":"corun-b","iters":760,"seconds":%s,"iters_per_sec":25.0,"device_open_seconds":0.01,"init_seconds":3.4,"start":"2026-01-01T00:01:00Z","end":"2026-01-01T00:01:30Z","compile_seconds":0,"compiled_from":"file"}' "$DURATION" > "$OUT/corun-b.json"
    SMI_PIDS=2; RUN_DONE=true
    return 0
  fi

  ORIG_SERVICE_STATE="$(node_ssh "systemctl is-active $SERVICE" 2>/dev/null || echo unknown)"
  record "service_initial_state" true "$SERVICE=$ORIG_SERVICE_STATE"
  if [ "$ORIG_SERVICE_STATE" = "active" ]; then
    node_ssh "sudo systemctl stop $SERVICE" 2>/dev/null || log "  경고: $SERVICE 정지 실패 — 계속 진행"
  fi

  # 게이트: 장치 DEVICE_ID 에 우리 것이 아닌 살아있는 컨텍스트가 남아 있으면 측정하지 않는다
  # (killed 상태 죽은 컨텍스트는 세지 않는다 — <rebellions-node> 사전 점검에서 상시 2개 확인됨).
  local ext_pids
  ext_pids="$(node_ssh "rbln-smi 2>/dev/null" 2>/dev/null | rbln_live_pid_count)" || ext_pids=0
  if [ "${ext_pids:-0}" != "0" ]; then
    set_blocked "ENVIRONMENT" "장치 ${DEVICE_ID} 에 외부 프로세스 ${ext_pids}개"
    return 1
  fi

  # descriptor 광고가 꺼져 있으면(advertise_check 가 이미 복원한 뒤라 보통 그렇다) 다시 켠다.
  if ! gen_advertised_n; then
    ncp_patch "{\"spec\":{\"$VENDOR\":{\"enabled\":false}}}"
    NCP_PATCHED=true
    poll "$T_DS_DELETE" "$IV" ncp_vendor_receded || log "  경고: $RESOURCE_VENDOR ${T_DS_DELETE}초 내 0-or-absent 아님(계속 진행)"
    kc apply -f "$DESC_FILE" >&2
    DESC_APPLIED=true
    poll "$T_GEN_UP" "$IV" gen_advertised_n || { set_blocked "ADVERTISE" "descriptor 광고 미성립"; return 1; }
  fi

  precompile_host || return 1

  kc -n "$CONSUMER_NS" create configmap "$CM_NAME" --from-file=workload.py="$WORKLOAD_FILE" --dry-run=client -o yaml | kc apply -f - >&2

  # solo
  kc -n "$CONSUMER_NS" apply -f "$(render_pod "$SCRIPT_DIR/pod-solo.yaml")" >&2
  poll "$POD_WAIT_SECONDS" "$IV" pod_done "$POD_SOLO" || { set_blocked "WORKLOAD" "solo 파드 미완료"; return 1; }
  result_json_from_pod "$POD_SOLO" "$OUT/solo.json" || { set_blocked "WORKLOAD" "solo RESULT 없음"; return 1; }

  # co-run: 같은 START_AT(배리어) 로 두 파드
  local start_at; start_at="$(python3 -c "import time;print(int(time.time())+$BARRIER_SECONDS)")"
  kc -n "$CONSUMER_NS" apply -f "$(render_pod "$SCRIPT_DIR/pod-a.yaml" "$start_at")" >&2
  kc -n "$CONSUMER_NS" apply -f "$(render_pod "$SCRIPT_DIR/pod-b.yaml" "$start_at")" >&2
  poll "$BARRIER_SECONDS" "$IV" both_running || log "  경고: 배리어 전에 둘 다 Running 이 아님(겹침은 verdict 가 판정)"

  # 측정 창 중간에 rbln-smi 로 같은 장치의 살아있는 서로 다른 PID 수를 기록한다(killed 제외).
  sleep "$(( DURATION / 2 ))"
  node_ssh "rbln-smi 2>/dev/null" > "$OUT/corun-smi.txt" || true
  SMI_PIDS="$(rbln_live_pid_count < "$OUT/corun-smi.txt")"

  poll "$POD_WAIT_SECONDS" "$IV" pod_done "$POD_A" || { set_blocked "WORKLOAD" "corun-a 미완료"; return 1; }
  poll "$POD_WAIT_SECONDS" "$IV" pod_done "$POD_B" || { set_blocked "WORKLOAD" "corun-b 미완료"; return 1; }
  result_json_from_pod "$POD_A" "$OUT/corun-a.json" || { set_blocked "WORKLOAD" "corun-a RESULT 없음"; return 1; }
  result_json_from_pod "$POD_B" "$OUT/corun-b.json" || { set_blocked "WORKLOAD" "corun-b RESULT 없음"; return 1; }

  # 측정 중 다른 계정이 서비스를 되살렸을 수 있다 — 판정과 무관하게 사실만 남긴다.
  local mid_expect="$ORIG_SERVICE_STATE"; [ "$ORIG_SERVICE_STATE" = "active" ] && mid_expect="inactive"
  local mid_now; mid_now="$(node_ssh "systemctl is-active $SERVICE" 2>/dev/null | head -1 || echo unknown)"
  [ "$mid_now" = "$mid_expect" ] || record "service_state_drift" true "측정 중 $SERVICE 상태가 예상과 다름(예상=$mid_expect 실제=$mid_now) — 외부 계정 가능성, 판정 무관"

  record "pods_completed" true "solo=$POD_SOLO a=$POD_A b=$POD_B smi_pids=$SMI_PIDS"
  RUN_DONE=true
}

# ── verdict: 겹침 먼저, 그다음 동시 진행 ─────────────────────────────────────
compute_verdict() {
  PHASE="verdict"
  if [ "$DRY_RUN" = "1" ]; then
    VERDICT_JSON='{"verdict":"dry","note":"DRY_RUN"}'
    record "concurrency_verdict" true "[DRY_RUN]"
    return 0
  fi
  VERDICT_JSON="$(DURATION="$DURATION" SMI_PIDS="${SMI_PIDS:-0}" MEASURE_MODE="$MEASURE_MODE" python3 - "$OUT/solo.json" "$OUT/corun-a.json" "$OUT/corun-b.json" <<'PY'
import sys, json, datetime, os
def load(p):
    try:
        s = open(p).read().strip(); return json.loads(s) if s else {}
    except Exception:
        return {}
s, ca, cb = [load(p) for p in sys.argv[1:4]]
def rate(d): return d.get("iters_per_sec")
rs, ra, rb = rate(s), rate(ca), rate(cb)
def num(x): return isinstance(x, (int, float))
out = {"r_solo": rs, "r_corun_a": ra, "r_corun_b": rb, "mode": os.environ.get("MEASURE_MODE", "host")}
if not all(num(x) for x in (rs, ra, rb)):
    out["verdict"] = "fail"; out["reason"] = "solo·corun 중 일부에 iters_per_sec 없음"
    print(json.dumps(out, ensure_ascii=False)); sys.exit(0)
def parse(t):
    return datetime.datetime.fromisoformat(t.replace("Z", "")) if t else None
try:
    sa, ea = parse(ca.get("start")), parse(ca.get("end"))
    sb, eb = parse(cb.get("start")), parse(cb.get("end"))
    overlap = (min(ea, eb) - max(sa, sb)).total_seconds()
    min_dur = min((ea - sa).total_seconds(), (eb - sb).total_seconds())
except Exception:
    overlap, min_dur = 0.0, 0.0
duration = float(os.environ.get("DURATION", "30"))
smi_pids = int(os.environ.get("SMI_PIDS", "0"))
# 겹침을 먼저 본다: 두 측정 창이 DURATION 의 80% 이상 겹쳐야 처리량을 비교한다.
overlap_ok = overlap >= 0.8 * duration and overlap > 0
ratio_a = ra / rs if rs else 0.0
ratio_b = rb / rs if rs else 0.0
corun_sum = ra + rb
sum_ratio = corun_sum / rs if rs else 0.0
# 동시 실행 성립: 겹쳤고 두 프로세스가 모두 실질적 진행(각 solo 의 0.3배 이상)을 냈다.
# (직렬화면 진 프로세스가 배리어를 놓쳐 error 로 죽거나 진행이 0 에 수렴한다 — Tenstorrent 선례.)
progress_a = ratio_a >= 0.3
progress_b = ratio_b >= 0.3
# 같은 장치 공유 확증: rbln-smi 가 측정 창에 NPU 에서 서로 다른 PID 2개를 봤다.
same_device = smi_pids >= 2
verdict = "pass" if (overlap_ok and progress_a and progress_b) else "fail"
reasons = []
if not overlap_ok: reasons.append("겹침 부족(overlap=%.1fs < DURATION 의 80%%=%.1fs, min_dur=%.1fs) — 처리량 비교 무의미" % (overlap, 0.8 * duration, min_dur))
if not progress_a: reasons.append("corun_a 진행 미달(%.3f×solo < 0.3)" % ratio_a)
if not progress_b: reasons.append("corun_b 진행 미달(%.3f×solo < 0.3)" % ratio_b)
if verdict == "pass" and not same_device:
    reasons.append("주의: rbln-smi 가 NPU 에서 PID 2개를 확인 못함(smi_pids=%d) — 같은 장치 공유는 DEVICE_ID 고정으로만 담보" % smi_pids)
out.update({
    "verdict": verdict,
    "r_solo": rs, "ratio_a": round(ratio_a, 4), "ratio_b": round(ratio_b, 4),
    "corun_sum": round(corun_sum, 4), "sum_ratio": round(sum_ratio, 4),
    "overlap_seconds": round(overlap, 2), "min_dur_seconds": round(min_dur, 2),
    "overlap_required_seconds": round(0.8 * duration, 2), "overlap_ok": overlap_ok,
    "device_open_a": ca.get("device_open_seconds"), "device_open_b": cb.get("device_open_seconds"),
    "init_a": ca.get("init_seconds"), "init_b": cb.get("init_seconds"),
    "smi_distinct_pids": smi_pids, "same_device_confirmed": same_device,
    "reason": "; ".join(reasons) if reasons else "겹치는 창에서 두 프로세스가 각각 solo 의 %.2f/%.2f 배 처리량으로 동시 진행(합 %.2f×solo) — 직렬화 아님" % (ratio_a, ratio_b, sum_ratio),
})
print(json.dumps(out, ensure_ascii=False))
PY
)"
  local v; v="$(printf '%s' "$VERDICT_JSON" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("verdict",""))' 2>/dev/null || echo "")"
  printf '%s\n' "$VERDICT_JSON" | tee "$OUT/verdict.json" >&2
  if [ "$v" = "pass" ]; then
    record "concurrency_verdict" true "$(printf '%s' "$VERDICT_JSON" | python3 -c 'import sys,json;d=json.load(sys.stdin);print("solo=%s a=%s b=%s sum_ratio=%s overlap=%ss open=%s/%s smi_pids=%s"%(d.get("r_solo"),d.get("ratio_a"),d.get("ratio_b"),d.get("sum_ratio"),d.get("overlap_seconds"),d.get("device_open_a"),d.get("device_open_b"),d.get("smi_distinct_pids")))')"
  else
    record "concurrency_verdict" false "$(printf '%s' "$VERDICT_JSON" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("reason",""))' 2>/dev/null || echo "verdict=$v")"
  fi
  if [ "$MEASURE_MODE" = "pods" ]; then
    if command -v jq >/dev/null 2>&1; then
      printf 'TIMESLICE_PODS: solo=%s a=%s b=%s sum=%s overlap=%s smi_pids=%s\n' \
        "$(jq -r '.r_solo' "$OUT/verdict.json")" "$(jq -r '.ratio_a' "$OUT/verdict.json")" "$(jq -r '.ratio_b' "$OUT/verdict.json")" \
        "$(jq -r '.sum_ratio' "$OUT/verdict.json")" "$(jq -r '.overlap_seconds' "$OUT/verdict.json")" "${SMI_PIDS:-0}"
    else
      SMI_PIDS="${SMI_PIDS:-0}" python3 -c '
import json, os, sys
d = json.load(open(sys.argv[1]))
print("TIMESLICE_PODS: solo=%s a=%s b=%s sum=%s overlap=%s smi_pids=%s" % (
    d.get("r_solo"), d.get("ratio_a"), d.get("ratio_b"), d.get("sum_ratio"),
    d.get("overlap_seconds"), os.environ.get("SMI_PIDS", "0")))
' "$OUT/verdict.json"
    fi
  fi
}

# ── cleanup: descriptor 제거 + 서비스 복원 확인 ─────────────────────────────
cleanup() {
  [ "$CLEANED" = true ] && return 0
  CLEANED=true
  PHASE="cleanup"
  log "### cleanup: 파드·ConfigMap 정리 + descriptor 제거 + $SERVICE 복원 확인"
  for p in "$POD_SOLO" "$POD_A" "$POD_B"; do kc -n "$CONSUMER_NS" delete pod "$p" --ignore-not-found --wait=false >&2; done
  kc -n "$CONSUMER_NS" delete configmap "$CM_NAME" --ignore-not-found >&2
  rm -f "$SCRIPT_DIR"/pod-*.yaml.rendered.yaml
  if [ "$DESC_APPLIED" = true ]; then
    kc delete acceleratordescriptor "$DESC_NAME" --ignore-not-found >&2 2>/dev/null || true
    DESC_APPLIED=false
    poll "$T_GC" "$IV" gen_ds_absent || log "  경고: $GEN_NS/$GEN_DS ${T_GC}초 내 미회수"
  fi
  # advertise_check 가 중단돼 벤더가 꺼진 채면 여기서 되돌린다(이중 안전).
  restore_vendor
  # node 트랩이 이미 서비스를 되살리지만, 여기서도 확인·복원한다(이중 안전). 시작값
  # (ORIG_SERVICE_STATE)으로만 복원한다 — <rebellions-node> 의 이 서비스는 다른 계정이 운영하므로 우리가
  # 멈춘 것만 다시 켠다(host·pods 모드 공통). 시작값을 확보하지 못했으면(예: preflight
  # 실행 전 중단) 안전하게 active 로 간주해 기존 동작을 유지한다.
  if [ "$DRY_RUN" != "1" ]; then
    local svc; svc="$(node_ssh "systemctl is-active $SERVICE" 2>/dev/null || echo unknown)"
    if [ "$svc" != "active" ] && [ "${ORIG_SERVICE_STATE:-active}" = "active" ]; then
      log "  $SERVICE=$svc — 시작값(active) 기준 복원 시도"
      node_ssh "sudo systemctl start $SERVICE" 2>/dev/null || log "  경고: $SERVICE 복원 실패 — 수동 확인 필요"
      sleep 3
      svc="$(node_ssh "systemctl is-active $SERVICE" 2>/dev/null || echo unknown)"
    fi
    SVC_FINAL="$svc"
    record "service_restored" "$([ "$svc" = "${ORIG_SERVICE_STATE:-active}" ] && echo true || echo false)" "$SERVICE=$svc(시작값=${ORIG_SERVICE_STATE:-?})"
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
  printf '{"test":"rebellions-timeslice","vendor":"%s","node":"%s","descriptor":"%s","resource":"%s","replicas":%s,"duration":%s,"deviceId":%s,"phase":"%s","classification":"%s","blockReason":"%s","checks":{%s},"fails":%s,"verdict":"%s","dryRun":%s,"concurrency":%s,"coRunInitSeconds":{"a":%s,"b":%s},"coRunDeviceOpenSeconds":{"a":%s,"b":%s},"smiDistinctPids":%s,"serviceFinal":"%s","date":"%s"}\n' \
    "$VENDOR" "$NODE" "$DESC_NAME" "$RESOURCE_DESC" "$REPLICAS" "$DURATION" "$DEVICE_ID" "$PHASE" "$CLASSIFICATION" "$BLOCK_REASON" "$CHECK_JSON" "$FAILS" "$verdict" "$dry" "${VERDICT_JSON:-null}" "${INIT_A:-null}" "${INIT_B:-null}" "${OPEN_A:-null}" "${OPEN_B:-null}" "${SMI_PIDS:-null}" "${SVC_FINAL:-unknown}" "$(utc_now)" \
    | tee -a "$OUT/summary-$(date +%H%M%S).json"
  [ "$verdict" = "pass" ] || [ "$verdict" = "dry" ] || exit 1
  exit 0
}

# PHASE=verdict OUT=<dir> 오프라인 재판정: trap 을 걸기 전에 세 json 만 읽어 판정하고 종료한다
# (클러스터·SSH 접근 0). REQUESTED_PHASE 는 위에서 내부 PHASE 상태변수가 "init" 등으로
# 덮어쓰기 전에 따로 캡처해 둔 사용자 요청값이다.
if [ "$REQUESTED_PHASE" = "verdict" ]; then
  [ -n "${OUT:-}" ] && [ -s "$OUT/solo.json" ] || { log "PHASE=verdict 는 OUT=<solo/corun json 이 있는 디렉터리> 가 필요하다"; exit 2; }
  MEASURE_MODE="$(python3 -c "import json;print(json.load(open('$OUT/verdict.json')).get('mode','host'))" 2>/dev/null || echo host)"
  [ -s "$OUT/corun-smi.txt" ] && SMI_PIDS="$(rbln_live_pid_count < "$OUT/corun-smi.txt")"
  compute_verdict
  v="$(printf '%s' "$VERDICT_JSON" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("verdict",""))' 2>/dev/null || echo "")"
  printf 'verdict=%s\n' "$v"
  [ "$v" = "pass" ] && exit 0
  exit 1
fi

trap finish EXIT
# 신호로 끊겨도 EXIT 트랩(정리)이 돌도록 신호를 종료 코드로 바꾼다(128+신호 번호) —
# 트랩이 없는 SIGTERM·SIGHUP 은 셸을 바로 끝내 EXIT 트랩이 돌지 않는다.
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

preflight || true
if [ "$BLOCKED" != true ]; then advertise_check || true; fi
if [ "$BLOCKED" != true ]; then measure || true; fi
if [ "$BLOCKED" != true ] && [ "$RUN_DONE" = true ]; then compute_verdict; fi
# EXIT trap(finish) 이 cleanup + verdict 라인 + JSON 요약 + 종료코드를 담당한다.
