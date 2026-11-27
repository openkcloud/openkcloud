#!/usr/bin/env bash
# ============================================================
# run.sh: AcceleratorHealth 건강 저하를 관측 경로로 보여주는 라이브 스크립트(주입 없음)
# 상세: 노드별 AcceleratorHealth 의 state/allocationAllowed/reason/signals/devices 를 표로 내고,
#       과거 evidence 가 세운 불변식(Healthy=할당 허용, 고장=할당 차단)이 성립하는지 본다(읽기 전용, 장애 미주입).
# 생성일: 2026-09-20 | 수정일: 2026-10-11
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 시나리오 성격(기록용). 이 스크립트는 상태를 바꾸지 않는다.
SCENARIO_DESTRUCTIVE="no"

DRY_RUN="${DRY_RUN:-0}"

# 과거 라이브 실측 레코드. EVIDENCE 로 경로를 주면 확인 한 줄을 남기고, 없으면 건너뛴다.
# 기본값을 두지 않는다 — 이 저장소 밖 경로를 기본값으로 박으면 받는 사람이 도달할 수 없다.
EVIDENCE="${EVIDENCE:-}"
EVIDENCE_CITE="${EVIDENCE:-2026-08-04 라이브 실측}"

OUT="${OUT:-/tmp/health-degraded-observe-evidence}"
mkdir -p "$OUT"

# 상태 변수.
DEMO_DONE=false
BLOCKED=false
CHECK_JSON=""
FAILS=0
OBJECTS=0
DEGRADED=0
PHASE="init"

utc_now() { date -u +%FT%TZ; }
log() { printf '%s\n' "$*" >&2; }

# kc: kubectl 래퍼. DRY_RUN 이면 명령만 찍고 실제로는 호출하지 않는다.
kc() {
  if [ "$DRY_RUN" = "1" ]; then
    printf '+ kubectl %s\n' "$*" >&2
    return 0
  fi
  kubectl "$@"
}

# 노드별 관측을 파싱해 표(stderr)와 검사 집계(stdout)를 내는 파이썬 분석기.
PY_ANALYZE=$(cat <<'PYEOF'
import json, sys
d = json.load(sys.stdin)
items = d.get("items", [])
broken = {"Unhealthy", "Quarantined", "Recovering"}
# 게이트 판정에서 뺄 신호. DeviceObservation(MIG 관측) 만 상태를 좌우하지 않는다 —
# internal/health/device.go:60-66 이 "MIG 관측 실패는 상태 무관"(GTX 970 은 MIG 미지원이라
# 관측이 영원히 실패하지만 GPU 는 정상)이라 명시한다. 나머지 신호(NodeReady·NDRFreshness·
# DriverHeartbeat·DevicePlugin·Advertisement·RepeatedFailure·DeviceDriver)는 실패 시
# internal/health/evaluate.go:81-150·device.go 에서 비-Healthy 로 조기 반환된다. 따라서
# Healthy 노드면 이 신호들은 전부 OK 여야 하고, DeviceObservation 실패는 정상 Healthy 와 공존한다.
NON_GATING = {"DeviceObservation"}
checks = {}
fails = 0
degraded = 0


def add(name, ok):
    global fails
    checks[name] = bool(ok)
    if not ok:
        fails += 1


objects = len(items)
w = sys.stderr.write
w("\n노드별 현재 AcceleratorHealth (관측 전용, 장애 주입 없음)\n")
w("%-24s %-11s %-6s %-18s %s\n" % ("NODE", "STATE", "ALLOC", "REASON", "FAILING SIGNALS"))
state_ok = True
sig_ok = True
gate_ok = True
for it in items:
    st = it.get("status", {}) or {}
    name = (it.get("metadata", {}) or {}).get("name", "?")
    state = st.get("state", "") or ""
    alloc = bool(st.get("allocationAllowed", False))
    reason = st.get("reason", "") or ""
    sigs = st.get("signals", []) or []
    failed = [s for s in sigs if not s.get("ok", False)]
    # 표시용: 모든 실패 신호(비게이트는 "(info)" 표기). 게이트용: 비게이트 신호 제외.
    bad = [s.get("name", "?") + ("@" + s["target"] if s.get("target") else "")
           + (" (info)" if s.get("name", "?") in NON_GATING else "")
           for s in failed]
    gating_bad = [s for s in failed if s.get("name", "?") not in NON_GATING]
    devs = st.get("devices", []) or []
    baddev = [dv.get("pciAddress", "?") + ":" + (dv.get("state", "") or "?")
              for dv in devs if (dv.get("state", "") not in ("", "Healthy"))]
    w("%-24s %-11s %-6s %-18s %s\n" % (name, state, str(alloc), reason[:18],
                                       ",".join(bad) if bad else "-"))
    if baddev:
        w("%-24s   devices: %s\n" % ("", ", ".join(baddev)))
    if not state:
        state_ok = False
    if not sigs:
        sig_ok = False
    if state == "Healthy" and ((not alloc) or gating_bad):
        gate_ok = False   # Healthy 인데 할당 차단·게이트 신호 실패 = 판정 불일치
    if state in broken:
        degraded += 1
        if alloc:
            gate_ok = False  # 고장인데 할당 허용 = 게이트 미작동
    if state in ("Degraded", "Unknown"):
        degraded += 1        # 관측만, 게이트는 단정하지 않음

add("ah_objects_present", objects > 0)
add("state_readable", state_ok and objects > 0)
add("signals_readable", sig_ok and objects > 0)
add("allocation_gate_consistent", gate_ok)

frag = ",".join('"%s":%s' % (k, "true" if v else "false") for k, v in checks.items())
sys.stdout.write("%s\t%d\t%d\t%d" % (frag, fails, objects, degraded))
PYEOF
)

# 과거 evidence 가 기록한 저하 전이 — 관측 표면(이 스크립트가 읽는 필드)이 저하 때 어떻게
# 움직이는지의 기준이다.
print_past_reference() {
  log ""
  log "과거 라이브 evidence 가 기록한 저하 전이"
  log "  T0        device-plugin 광고 붕괴 주입 (nvidia.com/gpu 4 → 0)"
  log "  +32s      state=Recovering, allocationAllowed=false  ← 신규 할당 차단(게이트 작동)"
  log "  +48s      state=Healthy 복귀"
  log "  +58s      광고 복원 (nvidia.com/gpu=4)"
  log "  복구 작업은 전 구간 정확히 1건 — 중복 생성 없음"
  if [ -f "$EVIDENCE" ]; then
    log "  evidence 파일 확인: $EVIDENCE"
  else
    log "  evidence 파일이 이 체크아웃에 없음 — 추적 경로 $EVIDENCE_CITE 를 참조(EVIDENCE 로 경로 지정 가능)"
  fi
}

# ── preflight: CRD 서빙·객체 존재 확인 ────────────────────────────────────
preflight() {
  PHASE="preflight"
  log "### preflight: AcceleratorHealth 관측 표면 확인"
  if [ "$DRY_RUN" = "1" ]; then
    log "  [DRY_RUN] kubectl get ah -o json 으로 CRD 서빙·객체 수 확인"
    return 0
  fi
  if ! kubectl get ah >/dev/null 2>&1; then
    log "BLOCKED: AcceleratorHealth(ah) CRD 가 서빙되지 않거나 접근 불가 — 관측 중단"
    BLOCKED=true
    return 1
  fi
}

# ── demo: 현재 관측 + 과거 대조 (읽기 전용 — 상태 변경 없음) ───────────────
# 세 함수 계약(preflight·demo·cleanup)에 맞춰 이름은 demo 지만 하는 일은 관측뿐이다.
# PHASE 는 관측 단계임을 그대로 남긴다.
demo() {
  PHASE="observe"
  print_past_reference
  if [ "$DRY_RUN" = "1" ]; then
    log ""
    log "### observe [DRY_RUN]: 아래 읽기만 수행한다(상태 변경 없음)"
    kc get ah -o json >/dev/null
    log "  이후 노드별 state/allocationAllowed/reason/signals/devices 를 표로 내고"
    log "  Healthy 노드 할당 허용·신호 정상, 고장 노드 할당 차단 불변식을 검사"
    DEMO_DONE=true
    return 0
  fi

  local res
  res="$(kubectl get ah -o json | python3 -c "$PY_ANALYZE")"
  IFS=$'\t' read -r CHECK_JSON FAILS OBJECTS DEGRADED <<<"$res"

  log ""
  log "관측 요약: 객체 ${OBJECTS}개, 현재 저하(비-Healthy) ${DEGRADED}개, 검사 실패 ${FAILS}건"
  if [ "$DEGRADED" -gt 0 ] 2>/dev/null; then
    log "  현재 저하된 노드가 있다 — 위 표의 STATE/REASON/FAILING SIGNALS 가 그 관측이다."
  else
    log "  현재 전 노드 정상 — 과거 evidence 의 복구 종점(Healthy·할당 허용)과 일치한다."
  fi
  DEMO_DONE=true
}

# ── cleanup: 읽기 전용이라 되돌릴 것이 없다 ───────────────────────────────
cleanup() {
  PHASE="cleanup"
  log "### cleanup: 관측 전용 — 되돌릴 상태 변경 없음"
}

# ── finish: JSON 요약 한 줄 + 종료코드 ────────────────────────────────────
finish() {
  cleanup || true
  local verdict="FAIL" dry="false"
  if [ "$DRY_RUN" = "1" ]; then
    verdict="DRY"; dry="true"
  elif [ "$BLOCKED" = true ]; then
    verdict="BLOCKED"
  elif [ "$DEMO_DONE" = true ] && [ "$FAILS" -eq 0 ]; then
    verdict="PASS"
  fi
  printf '{"test":"health-degraded-observe","scenario_destructive":"%s","objects":%s,"degraded":%s,"phase":"%s","checks":{%s},"fails":%s,"verdict":"%s","dryRun":%s,"date":"%s"}\n' \
    "$SCENARIO_DESTRUCTIVE" "${OBJECTS:-0}" "${DEGRADED:-0}" "$PHASE" "$CHECK_JSON" "${FAILS:-0}" "$verdict" "$dry" "$(utc_now)" \
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
# EXIT trap(finish) 이 JSON 요약 출력 + 종료코드를 담당한다.
