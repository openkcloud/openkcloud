#!/usr/bin/env bash
# ============================================================
# run.sh: Tenstorrent Blackhole SW 시분할 실 워크로드 처리량 라이브 하네스(<tenstorrent-node>)
# 상세: NPUClusterPolicy 를 끄고 descriptor 적용→측정(solo→co-run→solo)→판정→정리 순서로
#       돈다(판정 기준·배리어는 README 참고). 자체 descriptor·ConfigMap·파드·tenstorrent.enabled 만 변경한다.
# 생성일: 2026-09-22 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DRY_RUN="${DRY_RUN:-0}"
KCTX="${KUBECTL_CONTEXT:-kubernetes-admin@cluster.local}"

NAMESPACE="${NAMESPACE:-kcloud}"
NCP="${NCP:-npuclusterpolicy-sample}"
VENDOR="${VENDOR:-tenstorrent}"
NODE="${NODE:-k8s-worker3}"

NCP_DS_NS="${NCP_DS_NS:-kcloud}"
NCP_DS="${NCP_DS:-kcloud-tt-device-plugin}"
GEN_NS="${GEN_NS:-kcloud}"

DESC_FILE="${DESC_FILE:-$SCRIPT_DIR/descriptor.yaml}"
SOLO_FILE="${SOLO_FILE:-$SCRIPT_DIR/pod-solo.yaml}"
CORUN_A_FILE="${CORUN_A_FILE:-$SCRIPT_DIR/pod-corun-a.yaml}"
CORUN_B_FILE="${CORUN_B_FILE:-$SCRIPT_DIR/pod-corun-b.yaml}"
WORKLOAD_FILE="${WORKLOAD_FILE:-$SCRIPT_DIR/workload.py}"

DURATION="${DURATION:-60}"
# co-run 시작 배리어(초): 두 파드가 스케줄·기동·ttnn 초기화·워밍업을 마칠 시간을 준 뒤 같은
# START_AT 에 타이머를 동시에 시작하게 한다(측정 창 겹침 보장). 이 카드에서 배리어 초과는 튜닝
# 문제가 아니므로 이 값을 키우지 말 것 — tt-metal UMD 가 /dev/tenstorrent/0 에 KMD 락을 잡고
# 프로세스 수명 동안 유지해, 이긴 파드가 락을 쥔 채 배리어에서 대기하는 동안 진 파드는 그 락을
# 기다린다. 배리어를 키우면 진 파드의 대기가 그만큼 늘어 두 파드는 영영 겹치지 못한다(README
# "co-run 시작 배리어" 참조).
BARRIER_SECONDS="${BARRIER_SECONDS:-180}"
IMAGE="${IMAGE:-}"                 # 비어 있으면 매니페스트의 이미지 그대로 사용
CONFIGMAP="${CONFIGMAP:-tt-ts-workload}"
CONSUMER_NS="${CONSUMER_NS:-default}"

name_from() { sed -nE 's/^[[:space:]]*name:[[:space:]]*([^[:space:]]+).*/\1/p' "$1" | head -1; }
DESC_NAME="${DESC_NAME:-$(name_from "$DESC_FILE")}"
SOLO_NAME="${SOLO_NAME:-$(name_from "$SOLO_FILE")}"
CORUN_A_NAME="${CORUN_A_NAME:-$(name_from "$CORUN_A_FILE")}"
CORUN_B_NAME="${CORUN_B_NAME:-$(name_from "$CORUN_B_FILE")}"
GEN_DS="kcloud-generated-${DESC_NAME}-dp"

RESOURCE_DESC="${RESOURCE_DESC:-tenstorrent/blackhole-p150}"
RESOURCE_VENDOR="${RESOURCE_VENDOR:-tenstorrent.com/blackhole}"
REPLICAS="${REPLICAS:-2}"
PREPULL_NAME="tt-ts-prepull"

T_DS_DELETE="${T_DS_DELETE:-240}"
T_GEN_UP="${T_GEN_UP:-180}"
T_PREPULL="${T_PREPULL:-420}"
T_POD="${T_POD:-$((BARRIER_SECONDS + DURATION + 120))}"   # 배리어 대기 + 타이머 + 초기화 여유
T_POD_GONE="${T_POD_GONE:-120}"
T_GC="${T_GC:-180}"
T_RESTORE="${T_RESTORE:-300}"
IV="${IV:-5}"

OUT="${OUT:-/tmp/tt-timeslice-throughput-evidence}"
mkdir -p "$OUT"

RESOURCE_DESC_JP="${RESOURCE_DESC//./\\.}"
RESOURCE_VENDOR_JP="${RESOURCE_VENDOR//./\\.}"

ORIG_ENABLED=""
NCP_PATCHED=false
DESC_APPLIED=false
CM_APPLIED=false
APPLIED_PODS=""
RUN_DONE=false
BLOCKED=false
CLASSIFICATION=""
BLOCK_REASON=""
CLEANED=false
CHECK_JSON=""
FAILS=0
PHASE="init"
VERDICT_JSON=""
INIT_A=""   # co-run 파드 A 의 init_seconds(워크로드 INIT 로그에서 grep)
INIT_B=""   # co-run 파드 B 의 init_seconds

utc_now() { date -u +%FT%TZ; }
log() { printf '%s\n' "$*" >&2; }

kc() {
  if [ "$DRY_RUN" = "1" ]; then printf '+ kubectl --context %s %s\n' "$KCTX" "$*" >&2; return 0; fi
  kubectl --context "$KCTX" "$@"
}
kubeget() { if [ "$DRY_RUN" = "1" ]; then return 0; fi; kubectl --context "$KCTX" "$@"; }

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

ncp_patch() { kc -n "$NAMESPACE" patch npuclusterpolicy "$NCP" --type=merge -p "$1" >&2; }
ncp_ds_present() { kubeget -n "$NCP_DS_NS" get ds "$NCP_DS" >/dev/null 2>&1; }
ncp_ds_absent()  { ! ncp_ds_present; }
gen_ds_present() { kubeget -n "$GEN_NS" get ds "$GEN_DS" >/dev/null 2>&1; }
gen_ds_absent()  { ! gen_ds_present; }
ds_ready() { kubeget -n "$1" get ds "$2" -o jsonpath='{.status.numberReady}' 2>/dev/null || echo ""; }
alloc_desc()   { kubeget get node "$NODE" -o jsonpath="{.status.allocatable.$RESOURCE_DESC_JP}" 2>/dev/null || echo ""; }
alloc_vendor() { kubeget get node "$NODE" -o jsonpath="{.status.allocatable.$RESOURCE_VENDOR_JP}" 2>/dev/null || echo ""; }
ncp_ds_up() { local r; r="$(ds_ready "$NCP_DS_NS" "$NCP_DS")"; [ "${r:-0}" -ge 1 ] 2>/dev/null && [ -n "$(alloc_vendor)" ] && [ "$(alloc_vendor)" != "0" ]; }
gen_advertised_n() { local a; a="$(alloc_desc)"; [ -n "$a" ] && [ "$a" != "0" ] && [ "${a:-0}" -ge "$REPLICAS" ] 2>/dev/null; }
pod_phase() { kubeget -n "$CONSUMER_NS" get pod "$1" -o jsonpath='{.status.phase}' 2>/dev/null || echo ""; }
pod_terminal() { local p; p="$(pod_phase "$1")"; [ "$p" = "Succeeded" ] || [ "$p" = "Failed" ]; }
pod_active() { local p; p="$(pod_phase "$1")"; [ "$p" = "Running" ] || [ "$p" = "Succeeded" ] || [ "$p" = "Failed" ]; }
pod_absent() { ! kubeget -n "$CONSUMER_NS" get pod "$1" >/dev/null 2>&1; }
prepull_done() { local p; p="$(pod_phase "$PREPULL_NAME")"; [ "$p" = "Succeeded" ]; }
# 벤더 복원 뒤 operator 가 NDR 를 재검증할 때까지 잠시 걸린다(토글 off 순간 검증이 돌면 devicePlugin
# 단계가 allocatable=0 으로 실패해 passed 가 잠깐 false 로 남는다) — 한 번 읽지 말고 true 까지 poll.
ndr_validation_ok() { [ "$(kubeget get nodedevicereport "$NODE" -o jsonpath='{.status.validation.passed}' 2>/dev/null || echo "")" = "true" ]; }

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

# render_pod SRC DST [START_AT] — DURATION/IMAGE override 와 (co-run 때) START_AT 을 반영해
# 적용용 매니페스트를 만든다(PyYAML). START_AT 인자가 비면 solo 로 배리어 없이 즉시 시작한다.
render_pod() {
  local src="$1" dst="$2" start_at="${3:-}"
  if [ "$DRY_RUN" = "1" ]; then cp "$src" "$dst" 2>/dev/null || true; return 0; fi
  DURATION="$DURATION" IMAGE="$IMAGE" START_AT="$start_at" python3 - "$src" "$dst" <<'PY'
import os, sys, yaml
src, dst = sys.argv[1], sys.argv[2]
d = yaml.safe_load(open(src))
c = d["spec"]["containers"][0]
if os.environ.get("IMAGE"):
    c["image"] = os.environ["IMAGE"]
env = c.setdefault("env", [])
for e in env:
    if e["name"] == "DURATION":
        e["value"] = str(os.environ.get("DURATION", "60"))
start_at = os.environ.get("START_AT", "").strip()
if start_at:
    for e in env:
        if e["name"] == "START_AT":
            e["value"] = start_at
            break
    else:
        env.append({"name": "START_AT", "value": start_at})
yaml.safe_dump(d, open(dst, "w"), default_flow_style=False, sort_keys=False)
PY
}

pod_waiting_reason() {
  kubeget -n "$CONSUMER_NS" get pod "$1" -o jsonpath='{range .status.containerStatuses[*]}{.state.waiting.reason}{":"}{.state.waiting.message}{end}' 2>/dev/null || echo ""
}

# dump_log_tail LABEL LOGFILE — 실패 파드의 로그 끝부분을 stderr 로 남긴다(ANSI 제거).
# 정확한 ttnn/tt-metal 오류가 증거 레코드(evidence.sh 가 잡는 stdout+stderr)에 실리게 한다.
dump_log_tail() {
  log "--- $1 파드 로그 끝부분(정확한 오류) ---"
  sed 's/\x1b\[[0-9;]*m//g' "$2" 2>/dev/null | tail -20 >&2 || true
  log "--- $1 로그 끝 ---"
}

# collect_pod POD LABEL — 파드가 terminal 이 될 때까지 대기 후 RESULT json 을 $OUT/<label>.json 에
# 저장한다. 성공(iters_per_sec 존재) 0, 실패(미기동·error) 1 + set_blocked.
collect_pod() {
  local pod="$1" label="$2"
  local logf="$OUT/$label.log" resf="$OUT/$label.json"
  : > "$resf"
  if [ "$DRY_RUN" = "1" ]; then
    printf '{"phase":"%s","iters":999,"seconds":%s,"iters_per_sec":9.99,"start":"2026-01-01T00:00:00Z","end":"2026-01-01T00:01:00Z"}' "$label" "$DURATION" > "$resf"
    return 0
  fi
  if ! poll "$T_POD" "$IV" pod_terminal "$pod"; then
    local wr; wr="$(pod_waiting_reason "$pod")"
    kubectl --context "$KCTX" -n "$CONSUMER_NS" describe pod "$pod" > "$OUT/$label-describe.txt" 2>&1 || true
    set_blocked "ENVIRONMENT" "$label 파드($pod)가 ${T_POD}초 내 종료 상태 아님(phase=$(pod_phase "$pod") waiting=$wr)"
    record "${label}_completed" false "미종료 phase=$(pod_phase "$pod") waiting=$wr"
    return 1
  fi
  kubectl --context "$KCTX" -n "$CONSUMER_NS" logs "$pod" > "$logf" 2>/dev/null || true
  local line; line="$(grep '^RESULT ' "$logf" | tail -1 | sed 's/^RESULT //')"
  printf '%s' "$line" > "$resf"
  local ph; ph="$(pod_phase "$pod")"
  if [ -z "$line" ]; then
    dump_log_tail "$label" "$logf"
    # RESULT 없이 Failed = 파이썬 except 밖에서 죽음(C++ terminate/abort·SIGKILL 등). 로그에 장치
    # 오류 흔적(map::at·hang·pcie 등)이 있으면 HARDWARE, 아니면 소프트웨어·런타임(ENVIRONMENT).
    local cls; cls="ENVIRONMENT"
    if sed 's/\x1b\[[0-9;]*m//g' "$logf" 2>/dev/null | grep -qiE 'hang|pcie|link training|reset failed|arc.*(fail|error)|device.*(fault|hang)'; then cls="HARDWARE"; fi
    set_blocked "$cls" "$label 파드($pod) 로그에 RESULT 라인 없음(phase=$ph) — $(sed 's/\x1b\[[0-9;]*m//g' "$logf" 2>/dev/null | grep -iE 'terminate|error|exception|out_of_range|what\(\)' | tail -1 | cut -c1-160)"
    record "${label}_completed" false "RESULT 없음 phase=$ph"
    return 1
  fi
  if printf '%s' "$line" | grep -q '"error"'; then
    dump_log_tail "$label" "$logf"
    local errtxt cls; errtxt="$(printf '%s' "$line" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("error",""))' 2>/dev/null || echo "$line")"
    cls="ENVIRONMENT"
    if printf '%s' "$errtxt" | grep -qiE 'hardware|firmware|hang|pcie|link training|tt-smi|arc|reset failed|device.*(fault|error)'; then cls="HARDWARE"; fi
    set_blocked "$cls" "$label 파드($pod) workload 예외: $errtxt"
    record "${label}_completed" false "workload error: $(printf '%s' "$errtxt" | cut -c1-160)"
    return 1
  fi
  local rate; rate="$(printf '%s' "$line" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("iters_per_sec",""))' 2>/dev/null || echo "")"
  record "${label}_completed" true "iters_per_sec=$rate phase=$ph"
  return 0
}

# ── preflight ─────────────────────────────────────────────────────────────
preflight() {
  PHASE="preflight"
  log "### preflight (ctx=$KCTX vendor=$VENDOR node=$NODE desc=$DESC_NAME resource=$RESOURCE_DESC replicas=$REPLICAS duration=$DURATION)"
  if [ "$DRY_RUN" = "1" ]; then log "  [DRY_RUN] preflight 생략"; return 0; fi

  if ! kubectl --context "$KCTX" -n "$NAMESPACE" get npuclusterpolicy "$NCP" >/dev/null 2>&1; then
    set_blocked "ENVIRONMENT" "NPUClusterPolicy $NAMESPACE/$NCP 없음"; return 1
  fi
  ORIG_ENABLED="$(kubectl --context "$KCTX" -n "$NAMESPACE" get npuclusterpolicy "$NCP" -o jsonpath="{.spec.$VENDOR.enabled}" 2>/dev/null || echo "")"
  log "  원값: $VENDOR.enabled=$ORIG_ENABLED"
  local halloc; halloc="$(kubeget get node "$NODE" -o jsonpath='{.status.allocatable.hugepages-1Gi}' 2>/dev/null || echo "")"
  log "  시작: ncp_ds=$(ncp_ds_present && echo yes || echo no) ready=$(ds_ready "$NCP_DS_NS" "$NCP_DS") vendor_alloc=$(alloc_vendor) hugepages-1Gi=$halloc"
  record "start_vendor_advertised" "$([ -n "$(alloc_vendor)" ] && [ "$(alloc_vendor)" != "0" ] && echo true || echo false)" "$RESOURCE_VENDOR=$(alloc_vendor)"
  record "hugepages_1gi_present" "$([ -n "$halloc" ] && [ "$halloc" != "0" ] && echo true || echo false)" "allocatable=$halloc"
  if [ "$ORIG_ENABLED" != "true" ]; then
    set_blocked "ENVIRONMENT" "$VENDOR.enabled 이 true 가 아님(='$ORIG_ENABLED')"; return 1
  fi
}

# ── prepull: 타이밍 전에 파생 이미지를 노드에 미리 내려받는다 ──────
prepull() {
  PHASE="prepull"
  local img; img="$IMAGE"
  if [ -z "$img" ]; then img="$(sed -nE 's/^[[:space:]]*image:[[:space:]]*([^[:space:]]+).*/\1/p' "$SOLO_FILE" | head -1)"; fi
  log "### prepull: $NODE 에 이미지 미리 내려받기 ($img)"
  if [ "$DRY_RUN" = "1" ]; then log "+ (prepull pod $PREPULL_NAME on $NODE) $img"; record "image_prepulled" true "[DRY_RUN]"; return 0; fi
  kubectl --context "$KCTX" -n "$CONSUMER_NS" delete pod "$PREPULL_NAME" --ignore-not-found >&2 2>/dev/null || true
  cat <<EOF | kubectl --context "$KCTX" apply -f - >&2
apiVersion: v1
kind: Pod
metadata:
  name: $PREPULL_NAME
  namespace: $CONSUMER_NS
spec:
  restartPolicy: Never
  nodeSelector:
    kubernetes.io/hostname: $NODE
  containers:
    - name: c
      image: $img
      command: ["true"]
EOF
  APPLIED_PODS="$APPLIED_PODS $PREPULL_NAME"
  if poll "$T_PREPULL" "$IV" prepull_done; then
    record "image_prepulled" true "$PREPULL_NAME Succeeded"
  else
    record "image_prepulled" false "${T_PREPULL}초 내 미완료(phase=$(pod_phase "$PREPULL_NAME") waiting=$(pod_waiting_reason "$PREPULL_NAME"))"
  fi
  kubectl --context "$KCTX" -n "$CONSUMER_NS" delete pod "$PREPULL_NAME" --ignore-not-found >&2 2>/dev/null || true
}

# ── setup: 벤더 끄기 → ConfigMap → descriptor 광고 ──────────────────────────
setup_sharing() {
  PHASE="setup"
  log "### setup 1: $VENDOR.enabled=false → $NCP_DS_NS/$NCP_DS 회수"
  ncp_patch "{\"spec\":{\"$VENDOR\":{\"enabled\":false}}}"
  NCP_PATCHED=true
  if poll "$T_DS_DELETE" "$IV" ncp_ds_absent; then
    record "ncp_ds_deleted_on_disable" true "$NCP_DS_NS/$NCP_DS 삭제됨"
  else
    record "ncp_ds_deleted_on_disable" false "${T_DS_DELETE}초 내 미삭제"
  fi

  log "### setup 2: workload ConfigMap($CONFIGMAP) 생성"
  kc -n "$CONSUMER_NS" create configmap "$CONFIGMAP" --from-file=workload.py="$WORKLOAD_FILE" \
     --dry-run=client -o yaml | kc apply -f - >&2
  CM_APPLIED=true

  log "### setup 3: descriptor 적용 → 생성 device-plugin 이 $RESOURCE_DESC 을 $REPLICAS 개로 광고"
  kc apply -f "$DESC_FILE" >&2
  DESC_APPLIED=true
  if poll "$T_GEN_UP" "$IV" gen_advertised_n; then
    record "descriptor_advertised_n" true "$RESOURCE_DESC=$(alloc_desc) (gen_ds ready=$(ds_ready "$GEN_NS" "$GEN_DS"))"
  else
    record "descriptor_advertised_n" false "${T_GEN_UP}초 내 $REPLICAS 미광고(현재=$(alloc_desc) gen_ds ready=$(ds_ready "$GEN_NS" "$GEN_DS"))"
    kubectl --context "$KCTX" get acceleratordescriptor "$DESC_NAME" -o jsonpath='{"feasible="}{.status.feasibleBackends}{" refusals="}{range .status.refusals[*]}{.backend}{": "}{.reason}{" | "}{end}{"\n"}' 2>&1 | tee "$OUT/descriptor-status.txt" >&2 || true
    set_blocked "ENVIRONMENT" "descriptor 가 $RESOURCE_DESC 을 $REPLICAS 개로 광고하지 못함(현재=$(alloc_desc))"
    return 1
  fi
}

# ── measure: solo → co-run → solo ───────────────────────────────────────────
delete_pod_wait() {
  local pod="$1"
  kc -n "$CONSUMER_NS" delete pod "$pod" --ignore-not-found >&2 2>/dev/null || true
  poll "$T_POD_GONE" "$IV" pod_absent "$pod" || log "  경고: $pod ${T_POD_GONE}초 내 미삭제"
}

measure() {
  PHASE="measure-solo1"
  log "### measure solo1: $SOLO_NAME 적용 → 처리량"
  render_pod "$SOLO_FILE" "$OUT/pod-solo1.yaml"; kc apply -f "$OUT/pod-solo1.yaml" >&2
  APPLIED_PODS="$APPLIED_PODS $SOLO_NAME"
  collect_pod "$SOLO_NAME" "solo1" || return 1
  delete_pod_wait "$SOLO_NAME"

  PHASE="measure-corun"
  # 시작 배리어: 두 파드가 초기화·워밍업을 마칠 시간을 준 뒤 같은 START_AT 에 타이머를 동시에
  # 시작하게 해 측정 창이 겹치도록 한다(겹침은 사후 verdict 가 재검증). START_AT 은 epoch 초.
  local start_at
  start_at="$(python3 -c "import time; print(time.time() + $BARRIER_SECONDS)")"
  log "### measure co-run: $CORUN_A_NAME + $CORUN_B_NAME 동시 적용 → 같은 장치 처리량 (barrier=${BARRIER_SECONDS}s START_AT=$start_at)"
  render_pod "$CORUN_A_FILE" "$OUT/pod-corun-a.yaml" "$start_at"; render_pod "$CORUN_B_FILE" "$OUT/pod-corun-b.yaml" "$start_at"
  kc apply -f "$OUT/pod-corun-a.yaml" >&2
  kc apply -f "$OUT/pod-corun-b.yaml" >&2
  APPLIED_PODS="$APPLIED_PODS $CORUN_A_NAME $CORUN_B_NAME"
  # 둘 다 활성화된 순간 스냅샷(겹침 정황 증거; 엄밀한 겹침은 RESULT 타임스탬프로 판정).
  if [ "$DRY_RUN" != "1" ]; then
    ( for _ in $(seq 1 30); do
        if pod_active "$CORUN_A_NAME" && pod_active "$CORUN_B_NAME"; then
          kubectl --context "$KCTX" -n "$CONSUMER_NS" get pod "$CORUN_A_NAME" "$CORUN_B_NAME" -o wide > "$OUT/corun-wide.txt" 2>&1 || true
          break
        fi
        sleep 2
      done ) &
  fi
  local rc=0
  collect_pod "$CORUN_A_NAME" "corun-a" || rc=1
  collect_pod "$CORUN_B_NAME" "corun-b" || rc=1
  # 워크로드 INIT 로그에서 파드별 init_seconds 를 뽑아 summary 에 싣는다(블록 케이스에도 남긴다).
  INIT_A="$(grep -oE 'init_seconds=[0-9.]+' "$OUT/corun-a.log" 2>/dev/null | head -1 | cut -d= -f2)"
  INIT_B="$(grep -oE 'init_seconds=[0-9.]+' "$OUT/corun-b.log" 2>/dev/null | head -1 | cut -d= -f2)"
  log "  co-run init_seconds: a=${INIT_A:-?} b=${INIT_B:-?}"
  delete_pod_wait "$CORUN_A_NAME"
  delete_pod_wait "$CORUN_B_NAME"
  [ "$rc" -eq 0 ] || return 1

  PHASE="measure-solo2"
  log "### measure solo2: $SOLO_NAME 재적용 → 처리량(교대)"
  render_pod "$SOLO_FILE" "$OUT/pod-solo2.yaml"; kc apply -f "$OUT/pod-solo2.yaml" >&2
  APPLIED_PODS="$APPLIED_PODS $SOLO_NAME"
  collect_pod "$SOLO_NAME" "solo2" || return 1
  delete_pod_wait "$SOLO_NAME"

  RUN_DONE=true
}

# ── verdict: 4개 RESULT 로 처리량 판정 + 겹침 계산 ───────────────────────────
compute_verdict() {
  PHASE="verdict"
  if [ "$DRY_RUN" = "1" ]; then
    VERDICT_JSON='{"verdict":"dry","note":"DRY_RUN"}'
    record "throughput_verdict" true "[DRY_RUN]"
    return 0
  fi
  VERDICT_JSON="$(DURATION="$DURATION" python3 - "$OUT/solo1.json" "$OUT/corun-a.json" "$OUT/corun-b.json" "$OUT/solo2.json" <<'PY'
import sys, json, datetime, os
def load(p):
    try:
        s = open(p).read().strip()
        return json.loads(s) if s else {}
    except Exception:
        return {}
s1, ca, cb, s2 = [load(p) for p in sys.argv[1:5]]
def rate(d): return d.get("iters_per_sec")
r1, ra, rb, r2 = rate(s1), rate(ca), rate(cb), rate(s2)
def num(x): return isinstance(x, (int, float))
out = {"r_solo1": r1, "r_solo2": r2, "r_corun_a": ra, "r_corun_b": rb}
if not all(num(x) for x in (r1, ra, rb, r2)):
    out["verdict"] = "fail"; out["reason"] = "네 측정 중 일부에 iters_per_sec 없음"
    print(json.dumps(out)); sys.exit(0)
solo_mean = (r1 + r2) / 2.0
ratio_a = ra / solo_mean if solo_mean else 0.0
ratio_b = rb / solo_mean if solo_mean else 0.0
corun_sum = ra + rb
sum_ratio = corun_sum / solo_mean if solo_mean else 0.0
# 겹침(초): min(end) - max(start).
def parse(t):
    return datetime.datetime.fromisoformat(t.replace("Z", "")) if t else None
try:
    sa, ea = parse(ca.get("start")), parse(ca.get("end"))
    sb, eb = parse(cb.get("start")), parse(cb.get("end"))
    overlap = (min(ea, eb) - max(sa, sb)).total_seconds()
    min_dur = min((ea - sa).total_seconds(), (eb - sb).total_seconds())
except Exception:
    overlap, min_dur = 0.0, 0.0
# co-run 은 두 타이머 창이 DURATION 의 80% 이상 겹쳐야 유효(배리어의 5초 유예는 workload 쪽).
duration = float(os.environ.get("DURATION", "60"))
overlap_ok = overlap >= 0.8 * duration and overlap > 0
half_a = 0.35 <= ratio_a <= 0.65
half_b = 0.35 <= ratio_b <= 0.65
sum_ok = sum_ratio <= 1.1
verdict = "pass" if (half_a and half_b and sum_ok and overlap_ok) else "fail"
reasons = []
if not half_a: reasons.append("corun_a 가 solo 평균의 0.35~0.65 밖(%.3f)" % ratio_a)
if not half_b: reasons.append("corun_b 가 solo 평균의 0.35~0.65 밖(%.3f)" % ratio_b)
if not sum_ok: reasons.append("co-run 합이 solo 평균의 1.1배 초과(%.3f)" % sum_ratio)
if not overlap_ok: reasons.append("겹침 부족(overlap=%.1fs < DURATION 의 80%%=%.1fs, min_dur=%.1fs)" % (overlap, 0.8 * duration, min_dur))
out.update({
    "verdict": verdict,
    "solo_mean": round(solo_mean, 4),
    "ratio_a": round(ratio_a, 4), "ratio_b": round(ratio_b, 4),
    "corun_sum": round(corun_sum, 4), "sum_ratio": round(sum_ratio, 4),
    "overlap_seconds": round(overlap, 2), "min_dur_seconds": round(min_dur, 2),
    "overlap_required_seconds": round(0.8 * duration, 2),
    "overlap_ok": overlap_ok,
    "reason": "; ".join(reasons) if reasons else "co-run 각 파드 ≈0.5×solo, 합 ≤1.1×solo, 겹침 입증",
})
print(json.dumps(out, ensure_ascii=False))
PY
)"
  local v; v="$(printf '%s' "$VERDICT_JSON" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("verdict",""))' 2>/dev/null || echo "")"
  printf '%s\n' "$VERDICT_JSON" | tee "$OUT/verdict.json" >&2
  if [ "$v" = "pass" ]; then
    record "throughput_verdict" true "$(printf '%s' "$VERDICT_JSON" | python3 -c 'import sys,json;d=json.load(sys.stdin);print("solo_mean=%s ratio_a=%s ratio_b=%s sum_ratio=%s overlap=%ss"%(d.get("solo_mean"),d.get("ratio_a"),d.get("ratio_b"),d.get("sum_ratio"),d.get("overlap_seconds")))')"
  else
    record "throughput_verdict" false "$(printf '%s' "$VERDICT_JSON" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d.get("reason",""))' 2>/dev/null || echo "verdict=$v")"
  fi
}

# ── cleanup: 파드·descriptor·ConfigMap 제거 → GC → 벤더 복원 → NDR 확인 ──────
cleanup() {
  [ "$CLEANED" = true ] && return 0
  CLEANED=true
  PHASE="cleanup"
  log "### cleanup: 파드·descriptor·ConfigMap 제거 → GC → $VENDOR 재활성 복원"

  for p in $APPLIED_PODS; do
    kc -n "$CONSUMER_NS" delete pod "$p" --ignore-not-found >&2 2>/dev/null || true
  done
  APPLIED_PODS=""
  if [ "$CM_APPLIED" = true ]; then
    kc -n "$CONSUMER_NS" delete configmap "$CONFIGMAP" --ignore-not-found >&2 2>/dev/null || true
    CM_APPLIED=false
  fi
  if [ "$DESC_APPLIED" = true ]; then
    kc delete acceleratordescriptor "$DESC_NAME" --ignore-not-found >&2 2>/dev/null || true
    DESC_APPLIED=false
    if poll "$T_GC" "$IV" gen_ds_absent; then
      record "generated_dp_gc" true "$GEN_NS/$GEN_DS 회수됨"
    else
      record "generated_dp_gc" false "${T_GC}초 내 $GEN_NS/$GEN_DS 미회수"
    fi
  fi
  if [ "$NCP_PATCHED" = true ]; then
    ncp_patch "{\"spec\":{\"$VENDOR\":{\"enabled\":${ORIG_ENABLED:-true}}}}" 2>/dev/null || \
      log "  경고: 복원 패치 실패 — 수동 확인 필요"
    if poll "$T_RESTORE" "$IV" ncp_ds_up; then
      record "restored_vendor_dp" true "ready=$(ds_ready "$NCP_DS_NS" "$NCP_DS") $RESOURCE_VENDOR=$(alloc_vendor)"
    else
      record "restored_vendor_dp" false "${T_RESTORE}초 내 미복원(ready=$(ds_ready "$NCP_DS_NS" "$NCP_DS") $RESOURCE_VENDOR=$(alloc_vendor))"
    fi
    # NDR validation.passed 확인(복원 후 operator 재검증까지 poll).
    if poll "$T_RESTORE" "$IV" ndr_validation_ok; then
      record "ndr_validation_passed" true "validation.passed=true(재검증 반영)"
    else
      record "ndr_validation_passed" false "${T_RESTORE}초 내 validation.passed!=true(현재=$(kubeget get nodedevicereport "$NODE" -o jsonpath='{.status.validation.passed}' 2>/dev/null))"
    fi
  else
    log "  복원 불필요 — NPUClusterPolicy 를 패치한 적 없음"
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
  # evidence.sh 가 --expected "verdict=pass" 로 잡는 라인.
  printf 'verdict=%s\n' "$verdict"
  printf '{"test":"tt-timeslice-throughput","vendor":"%s","node":"%s","descriptor":"%s","resource":"%s","replicas":%s,"duration":%s,"phase":"%s","classification":"%s","blockReason":"%s","checks":{%s},"fails":%s,"verdict":"%s","dryRun":%s,"throughput":%s,"coRunInitSeconds":{"a":%s,"b":%s},"date":"%s"}\n' \
    "$VENDOR" "$NODE" "$DESC_NAME" "$RESOURCE_DESC" "$REPLICAS" "$DURATION" "$PHASE" "$CLASSIFICATION" "$BLOCK_REASON" "$CHECK_JSON" "$FAILS" "$verdict" "$dry" "${VERDICT_JSON:-null}" "${INIT_A:-null}" "${INIT_B:-null}" "$(utc_now)" \
    | tee -a "$OUT/summary-$(date +%H%M%S).json"
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
if [ "$BLOCKED" != true ]; then prepull; fi
if [ "$BLOCKED" != true ]; then setup_sharing || true; fi
if [ "$BLOCKED" != true ]; then measure || true; fi
if [ "$BLOCKED" != true ] && [ "$RUN_DONE" = true ]; then compute_verdict; fi
# EXIT trap(finish) 이 cleanup + verdict 라인 + JSON 요약 + 종료코드를 담당한다.
